package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// htmlPage is the shape html.duckduckgo.com/html/ serves: every hit is a
// result__a anchor pointing at the /l/?uddg= redirector, followed by its
// result__snippet blurb. The second block is a sponsored row (y.js), which must
// never reach the outline.
const htmlPage = `
<div class="result results_links results_links_deep web-result">
  <h2 class="result__title">
    <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&amp;rut=abc">The Go <b>docs</b></a>
  </h2>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F">Everything you need to know about <b>Go</b>&nbsp;&amp; friends.</a>
</div>
<div class="result result--ad">
  <a rel="nofollow" class="result__a" href="https://duckduckgo.com/y.js?ad_provider=x">Sponsored thing</a>
  <a class="result__snippet" href="https://duckduckgo.com/y.js">buy now</a>
</div>
<div class="result results_links">
  <h2 class="result__title">
    <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2F">pkg.go.dev</a>
  </h2>
  <a class="result__url" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2F">pkg.go.dev</a>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2F">Go package discovery.</a>
</div>
`

// litePage is the shape lite.duckduckgo.com/lite/ serves: a table whose hits
// link straight out and whose snippets live in their own cell.
const litePage = `
<table>
  <tr><td valign="top">1.&nbsp;</td>
      <td><a rel="nofollow" href="https://go.dev/doc/" class='result-link'>The Go docs</a></td></tr>
  <tr><td class='result-snippet'>Everything you need to know about Go.</td></tr>
  <tr><td valign="top">2.&nbsp;</td>
      <td class="result-link"><a rel="nofollow" href="https://pkg.go.dev/" class='result-link'>pkg.go.dev</a></td></tr>
  <tr><td class='result-snippet'>Go package discovery.</td></tr>
</table>
`

func TestParseHTMLShape(t *testing.T) {
	got := Parse(htmlPage, DefaultLimit)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (ads dropped): %+v", len(got), got)
	}
	if got[0].Title != "The Go docs" {
		t.Errorf("title = %q, want %q (tags stripped)", got[0].Title, "The Go docs")
	}
	if got[0].URL != "https://go.dev/doc/" {
		t.Errorf("url = %q, want the unwrapped uddg target", got[0].URL)
	}
	if want := "Everything you need to know about Go & friends."; got[0].Snippet != want {
		t.Errorf("snippet = %q, want %q (entities decoded)", got[0].Snippet, want)
	}
	if got[1].URL != "https://pkg.go.dev/" {
		t.Errorf("second url = %q", got[1].URL)
	}
	if got[1].Snippet != "Go package discovery." {
		t.Errorf("second snippet = %q — result__url must not eat the snippet slot", got[1].Snippet)
	}
}

func TestParseLiteShape(t *testing.T) {
	got := Parse(litePage, DefaultLimit)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	if got[0].URL != "https://go.dev/doc/" || got[0].Title != "The Go docs" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Snippet != "Go package discovery." {
		t.Errorf("second snippet = %q", got[1].Snippet)
	}
}

func TestParseLimitAndDedupe(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 25; i++ {
		b.WriteString(`<a class="result__a" href="https://example.com/` + string(rune('a'+i%5)) + `">hit</a>`)
	}
	if n := len(Parse(b.String(), 3)); n != 3 {
		t.Errorf("limit not honored: got %d, want 3", n)
	}
	// only five distinct urls exist in the 25 anchors
	if n := len(Parse(b.String(), DefaultLimit)); n != 5 {
		t.Errorf("dedupe by url: got %d, want 5", n)
	}
}

func TestParseGarbage(t *testing.T) {
	if got := Parse("<html><body>anomaly detected</body></html>", DefaultLimit); len(got) != 0 {
		t.Errorf("got %+v, want no results", got)
	}
}

func TestResolveURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"//duckduckgo.com/l/?uddg=https%3A%2F%2Fa.example%2Fx%3Fq%3D1&rut=z", "https://a.example/x?q=1"},
		{"https://plain.example/page", "https://plain.example/page"},
		{"https://duckduckgo.com/y.js?ad=1", ""},
		{"javascript:alert(1)", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := resolveURL(c.in); got != c.want {
			t.Errorf("resolveURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSearchFallsThroughToTheNextEndpoint(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>no results</html>"))
	}))
	defer empty.Close()
	var gotQuery string
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotQuery = r.Form.Get("q")
		w.Write([]byte(litePage))
	}))
	defer good.Close()

	c := &Client{Endpoints: []Endpoint{{URL: empty.URL}, {URL: good.URL}}}
	got, err := c.Search(context.Background(), " go docs ", DefaultLimit)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "go docs" {
		t.Errorf("posted q = %q, want the trimmed query", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
}

func TestSearchReportsFailure(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusTooManyRequests)
	}))
	defer down.Close()
	c := &Client{Endpoints: []Endpoint{{URL: down.URL}}}
	if _, err := c.Search(context.Background(), "go", DefaultLimit); err == nil {
		t.Fatal("want an error when every endpoint fails")
	}
	if _, err := c.Search(context.Background(), "   ", DefaultLimit); err == nil {
		t.Fatal("want an error for an empty query")
	}
}
