// Package websearch is the search backend behind the websearch node type: it
// asks a SearxNG instance for a query and hands back plain (title, url) pairs.
//
// SearxNG is the only backend, deliberately. It is the user's own metasearch —
// self-hosted or one they trust — so the query goes exactly where they pointed
// it and nowhere else, its JSON output is a real contract rather than scraped
// markup, and it needs no account and no API key. There is no fallback engine:
// if no instance can be found, a run says so instead of quietly asking someone
// else.
//
// The instance is named one of three ways, most explicit first — the editor's
// /settings searxng.url field, then ~/.config/lflow/credentials.json:
//
//	{"searxng": {"url": "https://searx.example.org"}}
//
// then LFLOW_SEARXNG_URL for a shell-scoped override. With none set the client
// tries the conventional local instance (localhost:8888) and, when nothing
// answers there either, reports that nothing is configured.
package websearch

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// LocalInstance is where a locally-run SearxNG lands by default (its own
// docker-compose and every install guide use this port), so a user running one
// on the same machine needs no configuration at all.
const LocalInstance = "http://localhost:8888/search"

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

// ErrNoInstance is what a run gets when no instance is configured and none is
// listening locally — the editor turns it into the red "error: searxng URL
// missing" status line (the /settings searxng.url field names one) rather than
// searching somewhere the user did not choose.
var ErrNoInstance = errors.New("searxng URL missing")

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
	return LocalInstance, false
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
			return nil, ErrNoInstance
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
