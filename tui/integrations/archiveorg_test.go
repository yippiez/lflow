package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// archivePage is an advancedsearch.php output=json answer, trimmed to the
// fields the node reads. The second doc carries the multi-valued shapes the
// index really returns (a list of titles, a list of creators) and the third has
// no title at all.
const archivePage = `{
  "responseHeader": {"status": 0},
  "response": {
    "numFound": 3,
    "docs": [
      {"identifier": "goodbye-blue-monday", "title": "Goodbye Blue Monday",
       "creator": "Kurt Vonnegut", "year": "1973", "mediatype": "texts"},
      {"identifier": "gd1977-05-08", "title": ["Grateful Dead Live at Barton Hall", "gd77-05-08"],
       "creator": ["Grateful Dead", "Betty Cantor"], "mediatype": "etree"},
      {"identifier": "untitled_item_042", "description": "a reel with no label"}
    ]
  }
}`

func TestParseArchiveOrg(t *testing.T) {
	got, err := ParseArchiveOrg(archivePage, DefaultLimit)
	if err != nil {
		t.Fatalf("ParseArchiveOrg: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(got), got)
	}
	if got[0].Title != "Goodbye Blue Monday" || got[0].URL != "https://archive.org/details/goodbye-blue-monday" {
		t.Errorf("first hit = %+v, want the item's title and /details/ page", got[0])
	}
	if got[0].Snippet != "Kurt Vonnegut · 1973 · texts" {
		t.Errorf("snippet = %q, want the item's provenance", got[0].Snippet)
	}
	// a multi-valued field reads as its leading value rather than failing the decode
	if got[1].Title != "Grateful Dead Live at Barton Hall" {
		t.Errorf("a list-valued title = %q", got[1].Title)
	}
	if !strings.HasPrefix(got[1].Snippet, "Grateful Dead · ") {
		t.Errorf("a list-valued creator = %q", got[1].Snippet)
	}
	// an untitled item is still a real hit: its identifier is its name
	if got[2].Title != "untitled_item_042" {
		t.Errorf("untitled hit = %+v, want the identifier as its title", got[2])
	}
	if _, err := ParseArchiveOrg("{not json", DefaultLimit); err == nil {
		t.Error("malformed json must be an error")
	}
}

// The limit is honored even when the API answers with more docs than asked for.
func TestParseArchiveOrgKeepsTheLimit(t *testing.T) {
	got, err := ParseArchiveOrg(archivePage, 2)
	if err != nil {
		t.Fatalf("ParseArchiveOrg: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
}

// A doc with no identifier has no page to link to, and a repeated identifier is
// the same item twice — neither becomes a row.
func TestParseArchiveOrgSkipsUnlinkableAndDuplicateDocs(t *testing.T) {
	body := `{"response":{"docs":[
		{"title":"no identifier here"},
		{"identifier":"nasa","title":"NASA Images"},
		{"identifier":"nasa","title":"NASA Images (again)"}
	]}}`
	got, err := ParseArchiveOrg(body, DefaultLimit)
	if err != nil {
		t.Fatalf("ParseArchiveOrg: %v", err)
	}
	if len(got) != 1 || got[0].URL != "https://archive.org/details/nasa" {
		t.Fatalf("results = %+v, want the one linkable item", got)
	}
}

// The request is archive.org's advanced search asking for JSON: the whole node
// name is the query, and the field list and row count are explicit.
func TestArchiveSearchAsksForJSON(t *testing.T) {
	var gotQuery url.Values
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotAccept = r.URL.Query(), r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(archivePage))
	}))
	defer srv.Close()

	c := &ArchiveClient{Endpoint: srv.URL}
	got, err := c.Search(context.Background(), "  vonnegut  ", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery.Get("q") != "vonnegut" {
		t.Errorf("q = %q, want the trimmed node name", gotQuery.Get("q"))
	}
	if gotQuery.Get("output") != "json" || gotAccept != "application/json" {
		t.Errorf("output = %q, Accept = %q", gotQuery.Get("output"), gotAccept)
	}
	if gotQuery.Get("rows") != "5" {
		t.Errorf("rows = %q, want the limit", gotQuery.Get("rows"))
	}
	if fl := gotQuery["fl[]"]; len(fl) != len(archiveFields) || fl[0] != "identifier" {
		t.Errorf("fl[] = %v, want the explicit field list", fl)
	}
	if len(got) != 3 {
		t.Fatalf("results = %+v", got)
	}
}

// A failing host names itself, so the status line says where the search died.
func TestArchiveSearchReportsTheHostOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := &ArchiveClient{Endpoint: srv.URL}
	_, err := c.Search(context.Background(), "vonnegut", DefaultLimit)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, want the failing status", err)
	}
}

// An answer with no docs is "nothing found here", not an empty success.
func TestArchiveSearchWithNoHits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":{"numFound":0,"docs":[]}}`))
	}))
	defer srv.Close()

	c := &ArchiveClient{Endpoint: srv.URL}
	_, err := c.Search(context.Background(), "vonnegut", DefaultLimit)
	if err == nil || !strings.Contains(err.Error(), "no results") {
		t.Fatalf("err = %v, want the no-results message", err)
	}
}

// The zero value searches archive.org itself: there is nothing to configure.
func TestArchiveClientNeedsNoConfiguration(t *testing.T) {
	c := &ArchiveClient{}
	if got := c.endpoint(); got != archiveEndpoint {
		t.Errorf("endpoint = %q, want %q", got, archiveEndpoint)
	}
	if _, err := c.Search(context.Background(), "   ", DefaultLimit); err == nil {
		t.Error("an empty search must not reach the network")
	}
}
