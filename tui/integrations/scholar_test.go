package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scholarPage is a SearxNG format=json answer from the google_scholar engine,
// trimmed to the fields a paper row reads — and deliberately ragged, because
// Scholar's green line is prose: the second hit has a publisher instead of a
// journal and its authors arrived as one string, the third has no citation
// fields at all, the fourth repeats the second's URL and the fifth is not a
// page.
const scholarPage = `{
  "query": "attention is all you need",
  "results": [
    {"url": "https://arxiv.org/abs/1706.03762", "title": "Attention is all you need",
     "content": "The dominant sequence transduction models...",
     "authors": ["A Vaswani", "N Shazeer", "N Parmar", "J Uszkoreit", "L Jones"],
     "journal": "Advances in neural information processing systems",
     "publishedDate": "2017-01-01T00:00:00"},
    {"url": "https://example.org/paper", "title": "An <b>earlier</b> idea",
     "content": "", "authors": "D Bahdanau, K Cho", "journal": null,
     "publisher": "Springer", "publishedDate": "2014"},
    {"url": "https://example.org/loose", "title": "No citation at all",
     "content": "", "authors": null, "publishedDate": null},
    {"url": "https://example.org/paper", "title": "the same paper, another engine"},
    {"url": "javascript:alert(1)", "title": "not a paper"}
  ]
}`

func TestParseScholar(t *testing.T) {
	got, err := ParseScholar(scholarPage, DefaultLimit)
	if err != nil {
		t.Fatalf("ParseScholar: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d papers, want 3 (duplicate and non-http dropped): %+v", len(got), got)
	}

	// a full hit: the citation names up to three authors, the journal and the year
	if got[0].Citation() != "A Vaswani, N Shazeer, N Parmar et al. · Advances in neural information processing systems · 2017" {
		t.Errorf("citation = %q", got[0].Citation())
	}
	if got[0].Snippet != "The dominant sequence transduction models..." {
		t.Errorf("snippet = %q", got[0].Snippet)
	}

	// authors as one string, publisher standing in for a missing journal, and a
	// year the engine never parsed into a date — every field a shape apart from
	// the first hit's, all of them read
	if got[1].Title != "An earlier idea" {
		t.Errorf("title = %q, want the engine's markup flattened", got[1].Title)
	}
	if got[1].Citation() != "D Bahdanau, K Cho · Springer · 2014" {
		t.Errorf("citation = %q", got[1].Citation())
	}

	// nothing known: a row shows its title alone rather than a lonely separator
	if got[2].Citation() != "" {
		t.Errorf("citation = %q, want empty when the engine gave no fields", got[2].Citation())
	}

	if _, err := ParseScholar("{not json", DefaultLimit); err == nil {
		t.Error("want an error for a malformed answer")
	}
}

// A scholar search is the general search narrowed to one engine: same
// instance, same JSON contract, google_scholar and nothing else.
func TestSearchScholarAsksTheScholarEngine(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotForm = r.Form.Encode()
		w.Write([]byte(scholarPage))
	}))
	defer srv.Close()

	c := &Client{Instance: srv.URL}
	got, err := c.SearchScholar(context.Background(), "attention is all you need", 2)
	if err != nil {
		t.Fatalf("SearchScholar: %v", err)
	}
	if !strings.Contains(gotForm, "engines="+ScholarEngine) {
		t.Errorf("posted form = %q, want engines=%s", gotForm, ScholarEngine)
	}
	if !strings.Contains(gotForm, "format=json") {
		t.Errorf("posted form = %q, want format=json", gotForm)
	}
	if len(got) != 2 {
		t.Fatalf("got %d papers, want the limit honored: %+v", len(got), got)
	}
	if got[0].URL != "https://arxiv.org/abs/1706.03762" {
		t.Errorf("first = %+v", got[0])
	}
}

// A general web search must not pick up the scholar narrowing — the two share
// a client and an instance, not a query.
func TestSearchDoesNotNarrowToAnEngine(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotForm = r.Form.Encode()
		w.Write([]byte(searxngPage))
	}))
	defer srv.Close()

	c := &Client{Instance: srv.URL}
	if _, err := c.Search(context.Background(), "go docs", DefaultLimit); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Contains(gotForm, "engines=") {
		t.Errorf("posted form = %q, want the instance's own engine set", gotForm)
	}
}

// An instance that answers an ordinary query with nothing is almost always one
// with google_scholar switched off, so the error has to name the engine rather
// than read as "Scholar knows no such paper".
func TestSearchScholarNamesTheDisabledEngine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"query": "x", "results": []}`))
	}))
	defer srv.Close()

	c := &Client{Instance: srv.URL}
	_, err := c.SearchScholar(context.Background(), "attention is all you need", DefaultLimit)
	if err == nil || !strings.Contains(err.Error(), ScholarEngine) {
		t.Fatalf("err = %v, want the engine named", err)
	}
}

// With no instance anywhere, a scholar run is a setup message — the same one a
// web run gets, never a query sent somewhere the user did not choose.
func TestSearchScholarWithoutAnInstance(t *testing.T) {
	c := &Client{ConfigDir: t.TempDir()} // the local default is not listening here
	_, err := c.SearchScholar(context.Background(), "attention is all you need", DefaultLimit)
	if err == nil || !strings.Contains(err.Error(), "searxng URL missing") {
		t.Fatalf("err = %v, want the missing-url message", err)
	}
}
