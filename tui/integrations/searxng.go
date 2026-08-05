// SearxNG: point lflow at your own metasearch instance and the websearch node
// asks it. The instance is named one of two ways, most explicit first — the
// editor's /settings searxng.url field, then
//
//	~/.config/lflow/credentials.json  {"searxng": {"url": "https://searx.example.org"}}
//
// Nothing else is needed: SearxNG's JSON output takes no key. The one
// requirement is on the instance — its settings.yml must list json in
// search.formats, which is off by default; fetch() names that explicitly when
// the instance answers 403.
//
// See the package doc (integrations.go) for the shared credential invariant.

package integrations

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// localInstance is where a locally-run SearxNG lands by default (its own
// docker-compose and every install guide use this port), so a user running one
// on the same machine needs no configuration at all.
const localInstance = "http://localhost:8888/search"

// DefaultLimit is how many hits a search keeps — the first page's top ten.
const DefaultLimit = 10

// maxBody caps a response read so a hostile or broken instance cannot balloon
// memory; a JSON answer is ~100KB.
const maxBody = 4 << 20

const userAgent = "lflow/websearch"

// Result is one web hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Client queries one SearxNG instance. The zero value is usable; ConfigDir
// points at the user's config root (credentials.json lives under it), and
// Instance/HTTP exist so tests point the client at a local server.
type Client struct {
	Instance  string
	ConfigDir string
	HTTP      *http.Client
}

// errNoInstance is what a run gets when no instance is configured and none is
// listening locally — the editor turns it into the red "error: searxng URL
// missing" status line (the /settings searxng.url field names one) rather than
// searching somewhere the user did not choose.
var errNoInstance = errors.New("searxng URL missing")

// searxngCredentials mirrors the searxng block of the consolidated
// credentials.json. The file is shared with other services (zotero and
// workflowy have their own blocks), so unknown keys are ignored rather than
// rejected.
type searxngCredentials struct {
	SearxNG struct {
		URL string `json:"url"`
	} `json:"searxng"`
}

// instance resolves where to ask: an explicit override (tests), the environment,
// credentials.json, and finally the conventional local instance. configured
// reports whether the user actually named it — a local instance that refuses the
// connection is "none found", not "your instance is broken".
func (c *Client) instance() (endpoint string, configured bool) {
	if c.Instance != "" {
		return searchPath(c.Instance), true
	}
	if u := searxngURL(c.ConfigDir); u != "" {
		return u, true
	}
	return localInstance, false
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// Search returns at most limit hits for query (limit <= 0 means DefaultLimit).
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty search")
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	endpoint, configured := c.instance()
	body, err := c.fetch(ctx, endpoint, query)
	if err != nil {
		if !configured && ctx.Err() == nil {
			// nothing was named and nothing answered on the local port: the user
			// has no instance, which is a setup message, not a search failure
			return nil, errNoInstance
		}
		return nil, err
	}
	hits, err := ParseSearxNG(body, limit)
	if err != nil {
		return nil, errors.Wrapf(err, "reading %s", hostOf(endpoint))
	}
	if len(hits) == 0 {
		return nil, errors.Errorf("no results from %s", hostOf(endpoint))
	}
	return hits, nil
}

// fetch POSTs the query to the instance and asks for its JSON output.
func (c *Client) fetch(ctx context.Context, endpoint, query string) (string, error) {
	form := url.Values{"q": {query}, "format": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.Wrap(err, "building the search request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
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
		// the usual misconfiguration: json is not in the instance's search.formats,
		// so it refuses the format=json field
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotAcceptable {
			return "", errors.Errorf("%s: %s — add json to the instance's search.formats",
				hostOf(endpoint), resp.Status)
		}
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

// searxngURL resolves the configured instance's /search endpoint, or "" when no
// instance is named. The /settings field is the in-app way in; credentials.json
// is the file-only way; there is deliberately no environment override — a URL
// configured in two places drifts, and the editor's own field covers scripting.
func searxngURL(configDir string) string {
	raw := ""
	if configDir != "" {
		raw = loadSearxNGConfig(configDir)
	}
	if raw == "" {
		return ""
	}
	return searchPath(raw)
}

// loadSearxNGConfig reads searxng.url from <configDir>/lflow/credentials.json.
// A missing or malformed file returns "" — the node then simply searches
// DuckDuckGo instead of failing cryptically.
func loadSearxNGConfig(configDir string) string {
	b, err := os.ReadFile(filepath.Join(configDir, "lflow", "credentials.json"))
	if err != nil {
		return ""
	}
	var c searxngCredentials
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return strings.TrimSpace(c.SearxNG.URL)
}

// searchPath completes an instance root to its query endpoint: a bare
// "https://searx.example.org" becomes ".../search", while a URL that already
// names a path (or carries query parameters — a preselected engine set, say)
// is left exactly as the user wrote it.
func searchPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	if p := strings.Trim(u.Path, "/"); p == "" {
		u.Path = "/search"
	}
	return u.String()
}

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// cleanText flattens a result field to plain text: an engine that slipped
// markup or entities through never reaches a node's name, because stored text
// carries no markup.
func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// resolveURL keeps http(s) targets and drops everything else — a result is a
// page to open, never a javascript: or data: payload.
func resolveURL(raw string) string {
	raw = html.UnescapeString(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

// searxngResponse is the slice of SearxNG's JSON answer the node needs.
type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// ParseSearxNG reads a SearxNG JSON answer, keeping the instance's own ranking.
// It is exported so the JSON contract is testable without an instance.
func ParseSearxNG(body string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	var resp searxngResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, errors.Wrap(err, "decoding the searxng answer")
	}
	var out []Result
	seen := map[string]bool{}
	for _, r := range resp.Results {
		if len(out) >= limit {
			break
		}
		link, title := resolveURL(r.URL), cleanText(r.Title)
		if link == "" || title == "" || seen[link] {
			continue
		}
		seen[link] = true
		out = append(out, Result{Title: title, URL: link, Snippet: cleanText(r.Content)})
	}
	return out, nil
}
