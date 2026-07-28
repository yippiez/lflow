// Package websearch is the free web-search backend behind the websearch node
// type: it asks a keyless search endpoint for a query and parses the answer
// into plain (title, url, snippet) triples.
//
// Two backends, both without an account or an API key:
//
//   - DuckDuckGo, the default. It has no official API, but it serves two
//     no-JavaScript result pages that need no key and no cookie —
//     html.duckduckgo.com/html/ and lite.duckduckgo.com/lite/, tried in that
//     order (the lite page covers the html one answering an interstitial). The
//     HTML parser reads either shape, so the node works out of the box.
//   - SearxNG, when the user names an instance (credentials.json searxng.url or
//     LFLOW_SEARXNG_URL). It is asked for `format=json` and its JSON results are
//     read straight through. A named instance is used EXCLUSIVELY — the whole
//     point of pointing lflow at your own metasearch is that the query does not
//     also go to DuckDuckGo, so there is no silent fallback.
//
// Nothing here touches the database: results are ephemeral, exactly like every
// other lflow run output.
package websearch

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// The keyless result endpoints, tried in this order.
const (
	HTMLEndpoint = "https://html.duckduckgo.com/html/"
	LiteEndpoint = "https://lite.duckduckgo.com/lite/"
)

// DefaultLimit is how many hits a search keeps — the first page's top ten.
const DefaultLimit = 10

// maxBody caps a response read so a hostile or broken endpoint cannot balloon
// memory; a result page is ~100KB.
const maxBody = 4 << 20

// userAgent is a plain desktop-browser string. The endpoints answer an empty
// page to an obviously scripted client, so the request has to look like a
// browser to get results at all.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// Result is one web hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Endpoint is one place to ask. SearxNG changes both the request (it is asked
// for JSON) and the policy (a named instance is never a step in a fallback
// chain that ends at DuckDuckGo).
type Endpoint struct {
	URL     string
	SearxNG bool
}

// Client queries the endpoints. The zero value is usable; ConfigDir points at
// the user's config root (credentials.json lives under it), and Endpoints/HTTP
// exist so tests point the client at a local server.
type Client struct {
	Endpoints []Endpoint
	ConfigDir string
	HTTP      *http.Client
}

// endpoints resolves where to ask, in precedence order:
//
//	explicit Endpoints (tests)
//	LFLOW_SEARXNG_URL / credentials.json searxng.url — that instance, alone
//	LFLOW_SEARCH_URL — one DuckDuckGo-shaped endpoint (a mirror, or a mock)
//	the two DuckDuckGo pages
func (c *Client) endpoints() []Endpoint {
	if len(c.Endpoints) > 0 {
		return c.Endpoints
	}
	if u := searxngURL(c.ConfigDir); u != "" {
		return []Endpoint{{URL: u, SearxNG: true}}
	}
	if u := strings.TrimSpace(os.Getenv("LFLOW_SEARCH_URL")); u != "" {
		return []Endpoint{{URL: u}}
	}
	return []Endpoint{{URL: HTMLEndpoint}, {URL: LiteEndpoint}}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// Search returns at most limit hits for query (limit <= 0 means DefaultLimit).
// Endpoints are tried in order and the first one that yields hits wins; an
// endpoint that errors or answers with no results falls through to the next.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty search")
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	var lastErr error
	for _, ep := range c.endpoints() {
		body, err := c.fetch(ctx, ep, query)
		if err != nil {
			if ctx.Err() != nil { // canceled/timed out — do not try the next one
				return nil, err
			}
			lastErr = err
			continue
		}
		hits, err := parseAny(body, limit)
		if err != nil {
			lastErr = errors.Wrapf(err, "reading %s", hostOf(ep.URL))
			continue
		}
		if len(hits) > 0 {
			return hits, nil
		}
		lastErr = errors.Errorf("no results from %s", hostOf(ep.URL))
	}
	if lastErr == nil {
		lastErr = errors.New("no results")
	}
	return nil, lastErr
}

// parseAny reads whichever answer arrived: a SearxNG JSON document, or a
// DuckDuckGo result page. Sniffing the body rather than trusting the endpoint's
// declared kind means a mirror that answers JSON works too.
func parseAny(body string, limit int) ([]Result, error) {
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		return ParseSearxNG(body, limit)
	}
	return Parse(body, limit), nil
}

// fetch POSTs the query as a form — the shape every endpoint here expects. A
// SearxNG instance is additionally asked for its JSON output.
func (c *Client) fetch(ctx context.Context, ep Endpoint, query string) (string, error) {
	form := url.Values{"q": {query}}
	accept := "text/html"
	if ep.SearxNG {
		form.Set("format", "json")
		accept = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.Wrap(err, "building the search request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	resp, err := c.http().Do(req)
	if err != nil {
		return "", errors.Wrapf(err, "calling %s", hostOf(ep.URL))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", errors.Wrapf(err, "reading %s", hostOf(ep.URL))
	}
	if resp.StatusCode != http.StatusOK {
		// the usual searxng misconfiguration: json is not in search.formats, and
		// the instance answers 403 to the format=json form field
		if ep.SearxNG && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotAcceptable) {
			return "", errors.Errorf("%s: %s — add json to the instance's search.formats",
				hostOf(ep.URL), resp.Status)
		}
		return "", errors.Errorf("%s: %s", hostOf(ep.URL), resp.Status)
	}
	return string(body), nil
}

func hostOf(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return endpoint
}

// An opening <a>/<td> tag. Both result pages mark their parts with classes
// rather than structure (result__a / result-link for the hit, result__snippet /
// result-snippet for its blurb), so a flat scan of opening tags in document
// order is enough to pair a snippet with the hit above it — and it survives the
// layout churn a DOM-shaped parser would not. Tags are scanned independently
// (rather than as element pairs) so a hit wrapped in a same-classed cell still
// reaches its own anchor, where the href lives.
var openTagRe = regexp.MustCompile(`(?is)<(a|td)\s([^>]*)>`)

var attrRe = regexp.MustCompile(`(?is)([a-zA-Z0-9_:-]+)\s*=\s*("[^"]*"|'[^']*'|[^\s">]+)`)

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// Parse extracts up to limit results from a DuckDuckGo result page (either the
// html or the lite shape). It is exported so the parsing contract is testable
// against captured pages without a network.
func Parse(body string, limit int) []Result {
	if limit <= 0 {
		limit = DefaultLimit
	}
	var out []Result
	seen := map[string]bool{}
	lower := strings.ToLower(body)
	// a skipped hit (an ad, a bare wrapper cell) also swallows its own snippet,
	// so a blurb never lands on the unrelated result above it
	dropSnippet := false
	for _, loc := range openTagRe.FindAllStringSubmatchIndex(body, -1) {
		name := strings.ToLower(body[loc[2]:loc[3]])
		attrs := parseAttrs(body[loc[4]:loc[5]])
		class := attrs["class"]
		if !isResultLink(class) && !isResultSnippet(class) {
			continue
		}
		inner := body[loc[1]:]
		if end := strings.Index(lower[loc[1]:], "</"+name+">"); end >= 0 {
			inner = inner[:end]
		}
		switch {
		case isResultLink(class):
			if len(out) >= limit {
				return out
			}
			link, title := resolveURL(attrs["href"]), cleanText(inner)
			if link == "" || title == "" || seen[link] {
				dropSnippet = true
				continue
			}
			seen[link] = true
			dropSnippet = false
			out = append(out, Result{Title: title, URL: link})
		case len(out) > 0 && !dropSnippet:
			if last := &out[len(out)-1]; last.Snippet == "" {
				last.Snippet = cleanText(inner)
			}
		}
	}
	return out
}

func isResultLink(class string) bool {
	return strings.Contains(class, "result__a") || strings.Contains(class, "result-link")
}

func isResultSnippet(class string) bool {
	return strings.Contains(class, "result__snippet") || strings.Contains(class, "result-snippet")
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		v := m[2]
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[strings.ToLower(m[1])] = v
	}
	return out
}

// resolveURL turns a result href into the destination. The html page wraps
// every hit in its own redirector (//duckduckgo.com/l/?uddg=<escaped>&rut=…),
// so the real target is unwrapped from the uddg parameter; the lite page links
// straight out. Sponsored rows (the y.js tracker) and non-http schemes are
// dropped by returning "".
func resolveURL(href string) string {
	href = html.UnescapeString(strings.TrimSpace(href))
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if target := u.Query().Get("uddg"); target != "" {
		if u, err = url.Parse(target); err != nil {
			return ""
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if strings.Contains(u.Path, "y.js") { // an ad slot, not a result
		return ""
	}
	return u.String()
}

// cleanText flattens result HTML (the <b> match highlights) to plain text —
// stored node text carries no markup, and neither does what we show.
func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
