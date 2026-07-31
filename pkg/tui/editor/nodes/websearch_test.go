package nodes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/websearch"
)

// searxAnswer is a SearxNG format=json answer with twelve hits, so the tests
// also prove the node keeps only the first ten.
func searxAnswer() string {
	type hit struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	var hits []hit
	for i := 0; i < 12; i++ {
		n := string(rune('a' + i))
		hits = append(hits, hit{URL: "https://" + n + ".example/page", Title: "Result " + n, Content: "about " + n})
	}
	b, _ := json.Marshal(map[string]any{"query": "go outliner", "results": hits})
	return string(b)
}

// serveInstance points the shared client at a local SearxNG stand-in.
func serveInstance(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, "nope", status)
			return
		}
		w.Write([]byte(body))
	}))
	prev := wsClient
	wsClient = websearch.Client{Instance: srv.URL}
	t.Cleanup(func() {
		wsClient = prev
		srv.Close()
	})
	return srv
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

func TestWebSearchHangsTheHitsAsChildNodes(t *testing.T) {
	serveInstance(t, searxAnswer(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}

	runToCompletion(t, h, n)

	if len(n.kids) != 10 {
		t.Fatalf("got %d children, want the first 10 hits", len(n.kids))
	}
	first := n.kids[0]
	if first.typ != database.TypeWebResult {
		t.Errorf("child type = %q, want the generated result type", first.typ)
	}
	if first.text != "Result a" || first.url != "https://a.example/page" {
		t.Errorf("first child = %q → %q, want the title linked to its result", first.text, first.url)
	}
	if n.kids[9].text != "Result j" {
		t.Errorf("last child = %q", n.kids[9].text)
	}
	if !strings.Contains(h.flash, "10 results") {
		t.Errorf("flash = %q", h.flash)
	}
}

// A re-run replaces the rows the last run made and leaves everything else — a
// note the user filed under the search, or a hit they moved out to keep.
func TestWebSearchRerunReplacesOnlyItsOwnRows(t *testing.T) {
	serveInstance(t, searxAnswer(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}
	mine := &fakeNode{uuid: "mine", typ: database.TypeBullets, text: "read these on the train", parent: n}
	n.kids = []*fakeNode{mine}

	runToCompletion(t, h, n)
	runToCompletion(t, h, n)

	if len(n.kids) != 11 {
		t.Fatalf("got %d children, want 10 fresh hits plus the user's row", len(n.kids))
	}
	if n.kids[10] != mine {
		t.Errorf("the user's own child must survive a re-run: %+v", n.kids[10])
	}
	for _, c := range n.kids[:10] {
		if c.typ != database.TypeWebResult {
			t.Errorf("row %q is not a generated result", c.text)
		}
	}
}

// The one hard requirement of a searxng-only backend: with no instance named
// and none listening locally, a run explains how to name one instead of asking
// anybody else.
func TestWebSearchWithoutAnInstanceErrors(t *testing.T) {
	prev := wsClient
	// an address nothing listens on stands in for "no local instance"
	wsClient = websearch.Client{}
	t.Cleanup(func() { wsClient = prev })
	t.Setenv("LFLOW_SEARXNG_URL", "")

	h := newFakeHost(t)
	h.configDir = t.TempDir() // no credentials.json
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}

	runToCompletion(t, h, n)

	st := wsStateOf(h, n.uuid)
	if !strings.Contains(st.errMsg, "no SearxNG instance") {
		t.Fatalf("err = %q, want the setup message", st.errMsg)
	}
	if !strings.Contains(st.errMsg, "credentials.json") {
		t.Errorf("the error must say where to name one: %q", st.errMsg)
	}
	if len(n.kids) != 0 {
		t.Errorf("a failed run must hang no rows, got %d", len(n.kids))
	}
	band := wsPreview(h, n, "", 120, false)
	if len(band) != 1 || !strings.Contains(ansi.Strip(band[0]), "no SearxNG instance") {
		t.Errorf("band = %v, want the error on the node", band)
	}
}

// The node reads the configured instance from the host's config dir on every
// run, so naming one in credentials.json takes effect without a restart.
func TestWebSearchUsesTheConfiguredInstance(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		asked = r.URL.Path + "?" + r.Form.Encode()
		w.Write([]byte(`{"results":[{"url":"https://searx.hit/","title":"From the instance","content":"a"}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"searxng": {"url": "` + srv.URL + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "lflow", "credentials.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	prev := wsClient
	wsClient = websearch.Client{}
	t.Cleanup(func() { wsClient = prev })

	h := newFakeHost(t)
	h.configDir = dir
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}
	runToCompletion(t, h, n)

	if st := wsStateOf(h, n.uuid); st.errMsg != "" {
		t.Fatalf("search failed: %s", st.errMsg)
	}
	if !strings.Contains(asked, "/search?") || !strings.Contains(asked, "format=json") {
		t.Errorf("asked %q, want the instance's json search endpoint", asked)
	}
	if len(n.kids) != 1 || n.kids[0].text != "From the instance" {
		t.Fatalf("children = %+v", n.kids)
	}
}

// A failed re-run keeps the rows the last good run produced: stale hits beat no
// hits, and the band says how old they are.
func TestWebSearchFailureKeepsTheLastRows(t *testing.T) {
	serveInstance(t, searxAnswer(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}
	runToCompletion(t, h, n)

	serveInstance(t, "", http.StatusForbidden)
	runToCompletion(t, h, n)

	if len(n.kids) != 10 {
		t.Errorf("got %d children, want the previous run's rows kept", len(n.kids))
	}
	st := wsStateOf(h, n.uuid)
	if !strings.Contains(st.errMsg, "search.formats") {
		t.Errorf("err = %q, want the searxng format hint", st.errMsg)
	}
}

// The stamp is the node's only persisted state: it lives in node_output, so the
// chip still reads right after a restart while the hits are just outline.
func TestWebSearchStampSurvivesAReload(t *testing.T) {
	serveInstance(t, searxAnswer(), http.StatusOK)
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}
	runToCompletion(t, h, n)

	if got := strings.TrimSpace(ansi.Strip(wsPreview(h, n, "", 120, false)[0])); got != "Last Updated: just now" {
		t.Fatalf("band = %q", got)
	}

	// a fresh host over the same database is what reopening the outline looks like
	reopened := &fakeHost{db: h.db, stores: map[string]map[string]any{}}
	band := wsPreview(reopened, n, "", 120, false)
	if len(band) != 1 || !strings.Contains(ansi.Strip(band[0]), "Last Updated:") {
		t.Errorf("band after a reload = %v, want the time chip", band)
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

func TestWebSearchBandWhileSearching(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "s1", typ: database.TypeWebSearch, text: "go outliner"}
	if band := wsPreview(h, n, "", 120, false); band != nil {
		t.Fatalf("an unrun node hangs no band, got %v", band)
	}
	wsStateOf(h, n.uuid).busy = true
	band := wsPreview(h, n, "", 120, false)
	if len(band) != 1 || !strings.Contains(ansi.Strip(band[0]), "searching") {
		t.Errorf("band = %v, want the in-flight line", band)
	}
}
