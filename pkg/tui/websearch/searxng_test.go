package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// searxngPage is a SearxNG /search?format=json answer, trimmed to the fields the
// node reads.
const searxngPage = `{
  "query": "go docs",
  "number_of_results": 2,
  "results": [
    {"url": "https://go.dev/doc/", "title": "The Go docs", "content": "Everything you need to know about Go.", "engine": "duckduckgo", "score": 3.0},
    {"url": "https://pkg.go.dev/", "title": "pkg.go.dev", "content": "Go package discovery.", "engine": "google", "score": 2.0},
    {"url": "https://go.dev/doc/", "title": "The Go docs (again)", "content": "a second engine found the same page", "engine": "brave", "score": 1.0},
    {"url": "javascript:alert(1)", "title": "not a page", "content": ""}
  ],
  "answers": [], "suggestions": []
}`

func TestParseSearxNG(t *testing.T) {
	got, err := ParseSearxNG(searxngPage, DefaultLimit)
	if err != nil {
		t.Fatalf("ParseSearxNG: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (duplicate and non-http dropped): %+v", len(got), got)
	}
	if got[0].Title != "The Go docs" || got[0].URL != "https://go.dev/doc/" {
		t.Errorf("first = %+v", got[0])
	}
	if got[0].Snippet != "Everything you need to know about Go." {
		t.Errorf("snippet = %q", got[0].Snippet)
	}
	if _, err := ParseSearxNG("{not json", DefaultLimit); err == nil {
		t.Error("want an error for a malformed answer")
	}
}

func TestSearchAsksSearxNGForJSON(t *testing.T) {
	var gotForm, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotForm, gotAccept = r.Form.Encode(), r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(searxngPage))
	}))
	defer srv.Close()

	c := &Client{Instance: srv.URL}
	got, err := c.Search(context.Background(), "go docs", DefaultLimit)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(gotForm, "format=json") {
		t.Errorf("posted form = %q, want format=json", gotForm)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if len(got) != 2 || got[0].URL != "https://go.dev/doc/" {
		t.Fatalf("results = %+v", got)
	}
}

// A 403 from an instance almost always means json is missing from its
// search.formats, so the error has to say so rather than read as "search broke".
func TestSearchNamesTheSearxNGFormatMisconfiguration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c := &Client{Instance: srv.URL}
	_, err := c.Search(context.Background(), "go docs", DefaultLimit)
	if err == nil || !strings.Contains(err.Error(), "search.formats") {
		t.Fatalf("err = %v, want the settings.yml hint", err)
	}
}

// Where the query goes, in precedence order — and whether the user actually
// named the place, which is what separates "your instance is down" from "you
// have no instance".
func TestInstanceResolution(t *testing.T) {
	dir := t.TempDir()

	// nothing named: the conventional local instance, and not "configured"
	c := &Client{ConfigDir: dir}
	if ep, configured := c.instance(); ep != LocalInstance || configured {
		t.Fatalf("default = %q configured=%v, want the local instance, unnamed", ep, configured)
	}

	// credentials.json: a bare root is completed to its /search endpoint
	writeCredentials(t, dir, `{"searxng": {"url": "https://searx.example.org"}}`)
	if ep, configured := c.instance(); ep != "https://searx.example.org/search" || !configured {
		t.Fatalf("from file = %q configured=%v", ep, configured)
	}

	// the environment wins over the file, and a url that already names a path
	// keeps it, query string and all
	t.Setenv("LFLOW_SEARXNG_URL", "https://from-env.example/search?engines=google")
	if ep, _ := c.instance(); ep != "https://from-env.example/search?engines=google" {
		t.Errorf("from env = %q, want the configured url untouched", ep)
	}
}

// With no instance anywhere, a run is a setup message — never a search sent to
// some other engine.
func TestSearchWithoutAnInstance(t *testing.T) {
	t.Setenv("LFLOW_SEARXNG_URL", "")
	// the local default is not listening in the test environment
	c := &Client{ConfigDir: t.TempDir()}
	_, err := c.Search(context.Background(), "go docs", DefaultLimit)
	if err == nil || !strings.Contains(err.Error(), "searxng URL missing") {
		t.Fatalf("err = %v, want the missing-url message", err)
	}
}

func TestSearxNGConfigIsOptional(t *testing.T) {
	dir := t.TempDir()
	if got := searxngURL(dir); got != "" {
		t.Errorf("no credentials.json → %q, want no instance", got)
	}
	writeCredentials(t, dir, `{"workflowy": {"api_key": "x"}}`)
	if got := searxngURL(dir); got != "" {
		t.Errorf("a file without a searxng block → %q", got)
	}
	writeCredentials(t, dir, `{ not json`)
	if got := searxngURL(dir); got != "" {
		t.Errorf("a malformed file → %q, want no instance", got)
	}
}

func writeCredentials(t *testing.T, configDir, body string) {
	t.Helper()
	dir := filepath.Join(configDir, "lflow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
