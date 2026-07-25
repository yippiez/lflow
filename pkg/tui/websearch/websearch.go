// Package websearch is the free web-search backend behind the websearch node
// type: it asks DuckDuckGo's KEYLESS result endpoints for a query and parses
// their HTML into plain (title, url, snippet) triples.
//
// DuckDuckGo has no official search API, but it serves two no-JavaScript result
// pages that need no account, no key and no cookie — html.duckduckgo.com/html/
// and lite.duckduckgo.com/lite/. Both are tried in order (the lite page is the
// fallback when the html one answers with an empty/interstitial page), and the
// parser reads either shape, so the node works out of the box for everyone.
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

// Client queries the endpoints. The zero value is usable; Endpoints and HTTP
// exist so tests point it at a local server speaking the same HTML.
type Client struct {
	Endpoints []string
	HTTP      *http.Client
}

// endpoints resolves the list to query: an explicit override, then the
// LFLOW_SEARCH_URL escape hatch (a self-hosted SearxNG/DDG mirror, or a mock
// during a demo), then the two DuckDuckGo pages.
func (c *Client) endpoints() []string {
	if len(c.Endpoints) > 0 {
		return c.Endpoints
	}
	if u := strings.TrimSpace(os.Getenv("LFLOW_SEARCH_URL")); u != "" {
		return []string{u}
	}
	return []string{HTMLEndpoint, LiteEndpoint}
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
		if hits := Parse(body, limit); len(hits) > 0 {
			return hits, nil
		}
		lastErr = errors.Errorf("no results from %s", hostOf(ep))
	}
	if lastErr == nil {
		lastErr = errors.New("no results")
	}
	return nil, lastErr
}

// fetch POSTs the query as a form — the shape both endpoints expect.
func (c *Client) fetch(ctx context.Context, endpoint, query string) (string, error) {
	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.Wrap(err, "building the search request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", errors.Wrapf(err, "calling %s", hostOf(endpoint))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", errors.Wrapf(err, "reading %s", hostOf(endpoint))
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf("%s: %s", hostOf(endpoint), resp.Status)
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
