package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// booksPage is a Google Books volumes answer, trimmed to the fields the node
// reads. It carries the shapes the parser has to survive: a subtitle, a
// two-author byline, a full publishedDate, an ISBN-10-only volume, a duplicate
// of the first volume (the same edition filed twice), and a titleless record.
const booksPage = `{
  "kind": "books#volumes",
  "totalItems": 412,
  "items": [
    {"id": "a", "volumeInfo": {
      "title": "Refactoring", "subtitle": "Improving the Design of Existing Code",
      "authors": ["Martin Fowler", "Kent Beck"], "publisher": "Addison-Wesley",
      "publishedDate": "1999-07-08", "pageCount": 448,
      "description": "A catalogue of refactorings.",
      "industryIdentifiers": [{"type": "ISBN_10", "identifier": "0201485672"},
                              {"type": "ISBN_13", "identifier": "9780201485677"}],
      "infoLink": "https://books.google.com/books?id=a&source=gbs_api",
      "canonicalVolumeLink": "https://books.google.com/books/about/Refactoring.html?id=a"}},
    {"id": "b", "volumeInfo": {
      "title": "The Go Programming Language", "authors": ["Alan A. A. Donovan"],
      "publishedDate": "2015", "industryIdentifiers": [{"type": "ISBN_10", "identifier": "0134190440"}],
      "canonicalVolumeLink": "https://books.google.com/books/about/Go.html?id=b"}},
    {"id": "a2", "volumeInfo": {
      "title": "Refactoring (ebook)",
      "canonicalVolumeLink": "https://books.google.com/books/about/Refactoring.html?id=a"}},
    {"id": "c", "volumeInfo": {"publishedDate": "2020"}}
  ]
}`

func TestParseGoogleBooks(t *testing.T) {
	got, err := ParseGoogleBooks(booksPage, BooksLimit)
	if err != nil {
		t.Fatalf("ParseGoogleBooks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d volumes, want 2 (duplicate and titleless dropped): %+v", len(got), got)
	}
	first := got[0]
	if first.Title != "Refactoring" || first.Subtitle != "Improving the Design of Existing Code" {
		t.Errorf("title/subtitle = %q / %q", first.Title, first.Subtitle)
	}
	if first.Byline() != "Martin Fowler and Kent Beck" {
		t.Errorf("byline = %q", first.Byline())
	}
	if first.Year != "1999" {
		t.Errorf("year = %q, want the year off a full publishedDate", first.Year)
	}
	if first.ISBN != "9780201485677" {
		t.Errorf("isbn = %q, want the ISBN-13", first.ISBN)
	}
	if first.Publisher != "Addison-Wesley" || first.Pages != 448 {
		t.Errorf("publisher/pages = %q / %d", first.Publisher, first.Pages)
	}
	if first.Link != "https://books.google.com/books/about/Refactoring.html?id=a" {
		t.Errorf("link = %q, want the canonical volume link", first.Link)
	}
	if got[1].ISBN != "0134190440" {
		t.Errorf("a volume with only an ISBN-10 must fall back to it, got %q", got[1].ISBN)
	}
	if got[1].Byline() != "Alan A. A. Donovan" {
		t.Errorf("single-author byline = %q", got[1].Byline())
	}
	if _, err := ParseGoogleBooks("{not json", BooksLimit); err == nil {
		t.Error("want an error for a malformed answer")
	}
}

// The limit is honored on the parse side, so a run keeps one screenful even if
// the endpoint answers with more.
func TestParseGoogleBooksHonorsTheLimit(t *testing.T) {
	var items []string
	for i := 0; i < 20; i++ {
		n := string(rune('a' + i))
		items = append(items, `{"volumeInfo":{"title":"Book `+n+`","canonicalVolumeLink":"https://books.google.com/`+n+`"}}`)
	}
	body := `{"items":[` + strings.Join(items, ",") + `]}`
	got, err := ParseGoogleBooks(body, 3)
	if err != nil {
		t.Fatalf("ParseGoogleBooks: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d volumes, want the first 3", len(got))
	}
}

// The search goes out as a GET with the query, a maxResults matching the limit
// and printType=books — a books node searches books, not magazines.
func TestBooksSearchAsksForVolumes(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]string{}
		for k := range r.URL.Query() {
			got[k] = r.URL.Query().Get(k)
		}
		got["method"] = r.Method
		w.Write([]byte(booksPage))
	}))
	defer srv.Close()

	c := &BooksClient{Endpoint: srv.URL}
	books, err := c.Search(context.Background(), "refactoring", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got["method"] != http.MethodGet {
		t.Errorf("method = %s, want GET", got["method"])
	}
	if got["q"] != "refactoring" || got["maxResults"] != "5" || got["printType"] != "books" {
		t.Errorf("query = %+v", got)
	}
	if _, ok := got["key"]; ok {
		t.Errorf("an unconfigured client must search anonymously, sent key=%q", got["key"])
	}
	if len(books) != 2 || books[0].Title != "Refactoring" {
		t.Fatalf("books = %+v", books)
	}
}

// The optional api_key in credentials.json rides along when it is there — it
// only raises the quota, so its absence is never an error.
func TestBooksSearchSendsTheCredentialsKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		w.Write([]byte(booksPage))
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeBooksCredentials(t, dir, `{"googlebooks": {"api_key": "quota-key"}, "workflowy": {"api_key": "other"}}`)

	c := &BooksClient{Endpoint: srv.URL, ConfigDir: dir}
	if _, err := c.Search(context.Background(), "refactoring", 0); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotKey != "quota-key" {
		t.Errorf("key = %q, want the one from the googlebooks block", gotKey)
	}
}

// A throttled search names the fix — the quota is per IP and the api_key that
// raises it is not something a bare 429 would suggest.
func TestBooksSearchNamesTheQuotaFix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := &BooksClient{Endpoint: srv.URL}
	_, err := c.Search(context.Background(), "refactoring", 0)
	if err == nil {
		t.Fatal("want an error for a throttled search")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error = %q, want it to name the api_key fix", err)
	}
}

// A search nobody wrote a term for, and one nothing matched, are both errors
// rather than an empty run that silently wipes the previous rows.
func TestBooksSearchEmptyCases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"totalItems": 0}`))
	}))
	defer srv.Close()

	c := &BooksClient{Endpoint: srv.URL}
	if _, err := c.Search(context.Background(), "   ", 0); err == nil {
		t.Error("want an error for an empty search")
	}
	_, err := c.Search(context.Background(), "asdkjhasdkjh", 0)
	if err == nil || !strings.Contains(err.Error(), "no books found") {
		t.Errorf("error = %v, want the no-books message", err)
	}
}

func TestLoadBooksAPIKey(t *testing.T) {
	dir := t.TempDir()
	if got := LoadBooksAPIKey(dir); got != "" {
		t.Errorf("no credentials file must read as no key, got %q", got)
	}
	writeBooksCredentials(t, dir, "{not json")
	if got := LoadBooksAPIKey(dir); got != "" {
		t.Errorf("a malformed file must read as no key, got %q", got)
	}
	writeBooksCredentials(t, dir, `{"googlebooks": {"api_key": "  k  "}}`)
	if got := LoadBooksAPIKey(dir); got != "k" {
		t.Errorf("key = %q", got)
	}
}

// bookYear takes the year off whatever shape the publisher filed.
func TestBookYear(t *testing.T) {
	for raw, want := range map[string]string{
		"2004": "2004", "2004-05": "2004", "2004-05-01": "2004",
		"": "", "n.d.": "", "199": "",
	} {
		if got := bookYear(raw); got != want {
			t.Errorf("bookYear(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A volume's fields reach the node as plain text: markup an entry slipped into
// its title never lands in a node name.
func TestParseGoogleBooksFlattensMarkup(t *testing.T) {
	body, err := json.Marshal(map[string]any{"items": []any{map[string]any{
		"volumeInfo": map[string]any{
			"title":               "<b>Refactoring</b>&nbsp;II",
			"canonicalVolumeLink": "https://books.google.com/x",
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseGoogleBooks(string(body), BooksLimit)
	if err != nil {
		t.Fatalf("ParseGoogleBooks: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Refactoring II" {
		t.Fatalf("title = %+v", got)
	}
}

func writeBooksCredentials(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "lflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lflow", "credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
