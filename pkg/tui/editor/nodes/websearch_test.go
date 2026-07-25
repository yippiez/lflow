package nodes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/websearch"
)

// resultPage is a lite-shaped DuckDuckGo answer with twelve hits, so the tests
// also prove the node keeps only the first ten.
func resultPage() string {
	var b strings.Builder
	for i := 1; i <= 12; i++ {
		n := string(rune('a' + i - 1))
		b.WriteString(`<tr><td><a rel="nofollow" href="https://www.` + n + `.example/page" class='result-link'>Result ` + n + `</a></td></tr>`)
		b.WriteString(`<tr><td class='result-snippet'>about ` + n + `</td></tr>`)
	}
	return b.String()
}

// serveResults points the shared client at a local server for the test's life.
func serveResults(t *testing.T, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, "nope", status)
			return
		}
		w.Write([]byte(body))
	}))
	prev := wsClient
	wsClient = websearch.Client{Endpoints: []string{srv.URL}}
	t.Cleanup(func() {
		wsClient = prev
		srv.Close()
	})
}

// runToCompletion performs an alt+r and folds the resulting message back in, the
// way the editor's update loop does.
func runToCompletion(t *testing.T, h *fakeHost, n *fakeNode) {
	t.Helper()
	cmd := runWebSearch(h, n)
	if cmd == nil {
		t.Fatal("alt+r produced no command")
	}
	msg, ok := cmd().(wsDoneMsg)
	if !ok {
		t.Fatalf("unexpected message type %T", cmd())
	}
	msg.HandleNodePlugin(h)
}

func TestWebSearchRunKeepsTenResults(t *testing.T) {
	serveResults(t, resultPage(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}

	runToCompletion(t, h, n)

	st := wsStateOf(h, n.uuid)
	if st.busy {
		t.Error("still busy after the search finished")
	}
	if len(st.results) != 10 {
		t.Fatalf("kept %d results, want the first 10", len(st.results))
	}
	if st.results[0].Title != "Result a" || st.results[0].URL != "https://www.a.example/page" {
		t.Errorf("first result = %+v", st.results[0])
	}
	if a, _ := h.NodeStore(n.uuid)["animating"].(bool); a {
		t.Error("the animation flag outlived the search")
	}
	if !strings.Contains(h.flash, "10 results") {
		t.Errorf("flash = %q, want the result count", h.flash)
	}
}

func TestWebSearchPreviewShowsTheTenHits(t *testing.T) {
	serveResults(t, resultPage(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}

	if bands := wsPreview(h, n, "", 120, false); bands != nil {
		t.Fatalf("an unrun node hangs no bands, got %v", bands)
	}
	runToCompletion(t, h, n)

	bands := wsPreview(h, n, "", 120, false)
	if len(bands) != 11 {
		t.Fatalf("got %d bands, want a header plus 10 hits", len(bands))
	}
	if !strings.Contains(ansi.Strip(bands[0]), "10 results") {
		t.Errorf("header = %q", ansi.Strip(bands[0]))
	}
	first := ansi.Strip(bands[1])
	if !strings.Contains(first, "1 Result a") || !strings.Contains(first, "a.example") {
		t.Errorf("first hit band = %q, want its rank, title and host", first)
	}
	if got := ansi.Strip(bands[10]); !strings.Contains(got, "10 Result j") {
		t.Errorf("last hit band = %q", got)
	}
	if bands := wsPreview(h, n, "", 120, true); bands != nil {
		t.Error("the preview must yield to the focused view")
	}
}

func TestWebSearchPreviewFlagsAnEditedQuery(t *testing.T) {
	serveResults(t, resultPage(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}
	runToCompletion(t, h, n)

	n.text = "go outliner tui"
	head := ansi.Strip(wsPreview(h, n, "", 120, false)[0])
	if !strings.Contains(head, "go outliner") || !strings.Contains(head, "⌥r re-runs") {
		t.Errorf("header = %q, want the stale query named", head)
	}
}

func TestWebSearchReportsFailure(t *testing.T) {
	serveResults(t, "", http.StatusTooManyRequests)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}

	runToCompletion(t, h, n)

	st := wsStateOf(h, n.uuid)
	if st.errMsg == "" {
		t.Fatal("want the failure recorded on the node")
	}
	bands := wsPreview(h, n, "", 120, false)
	if len(bands) != 1 || !strings.Contains(ansi.Strip(bands[0]), "websearch ·") {
		t.Errorf("bands = %v, want one error line", bands)
	}
	if !strings.Contains(h.flash, "websearch ·") {
		t.Errorf("flash = %q", h.flash)
	}
}

func TestWebSearchRefusesAnEmptyQuery(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "   "}
	if cmd := runWebSearch(h, n); cmd != nil {
		t.Error("an empty row must not reach the network")
	}
	if !strings.Contains(h.flash, "type what to search") {
		t.Errorf("flash = %q", h.flash)
	}
}

func TestWebSearchViewScrollsAndMatchesItsLineCount(t *testing.T) {
	serveResults(t, resultPage(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}
	runToCompletion(t, h, n)

	v := wsView{}
	if !v.Enter(h, n) {
		t.Fatal("the view declined to open")
	}
	total := v.Lines(h, n, 100)
	if want := 1 + 3*10; total != want {
		t.Fatalf("Lines = %d, want %d (header + title/url/snippet per hit)", total, want)
	}
	if got := len(v.Bands(h, n, "", 100, 0, total, true)); got != total {
		t.Errorf("Bands rendered %d lines, want the %d Lines promised", got, total)
	}
	if _, handled := v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}); !handled {
		t.Fatal("j must scroll")
	}
	if h.scroll != 1 {
		t.Errorf("scroll = %d, want 1", h.scroll)
	}
	body := v.Bands(h, n, "", 100, h.scroll, 3, true)
	if len(body) != 3 || !strings.Contains(ansi.Strip(body[0]), "Result a") {
		t.Errorf("scrolled window = %v", body)
	}
	if _, handled := v.Key(h, n, tea.KeyMsg{Type: tea.KeyEsc}); handled {
		t.Error("esc must fall through to the central defocus")
	}
}

func TestWebSearchContext(t *testing.T) {
	serveResults(t, resultPage(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}
	runToCompletion(t, h, n)

	tag, _, body := wsToContext(h, n)
	if tag != "websearch" {
		t.Errorf("tag = %q", tag)
	}
	if !strings.HasPrefix(body, "go outliner\n") {
		t.Errorf("body must open with the query: %q", body)
	}
	if n := strings.Count(body, "<result url="); n != 10 {
		t.Errorf("got %d result elements, want 10", n)
	}
}
