package websearch

import (
	"encoding/json"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

// SearxNG support: point lflow at your own metasearch instance and the websearch
// node asks it. The instance is named one of two ways, most explicit first —
// the editor's /settings searxng.url field, then
//
//	~/.config/lflow/credentials.json  {"searxng": {"url": "https://searx.example.org"}}
//
// Nothing else is needed: SearxNG's JSON output takes no key. The one
// requirement is on the instance — its settings.yml must list json in
// search.formats, which is off by default; fetch() names that explicitly when
// the instance answers 403.
//
// WARNING (invariant): credentials.json is local-only config — it is never
// written into the outline DB and never synced (see pkg/tui/wf/credentials.go).

// credentials mirrors the searxng block of the consolidated credentials.json.
// The file is shared with other services (workflowy has its own block), so
// unknown keys are ignored rather than rejected.
type credentials struct {
	SearxNG struct {
		URL string `json:"url"`
	} `json:"searxng"`
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
	var c credentials
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
