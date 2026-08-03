package editor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/websearch"
)

// searxAnswer is a SearxNG format=json answer with twelve hits, so the tests
// also prove a web run keeps only the first ten.
func searxAnswer() string {
	var b strings.Builder
	b.WriteString(`{"query":"go docs","results":[`)
	for i := 0; i < 12; i++ {
		n := string(rune('a' + i))
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"url":"https://` + n + `.example/page","title":"Result ` + n + `","content":"about ` + n + `"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// serveWeb points the shared client at a local SearxNG stand-in.
func serveWeb(t *testing.T, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, "nope", status)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := wsClient
	wsClient = websearch.Client{Instance: srv.URL}
	t.Cleanup(func() { wsClient = prev })
}

// pumpWeb drives a web node's alt+r to completion the way the editor's update
// loop does: run the command, land its webDoneMsg.
func pumpWeb(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	wd, ok := msg.(webDoneMsg)
	if !ok {
		t.Fatalf("run command returned %T, want webDoneMsg", msg)
	}
	m.handleWebDone(wd)
}

// A web node searches the WEB, not the outline: alt+r hangs the first ten hits
// as link-chip rows, and nothing else.
func TestWebNodeHangsHits(t *testing.T) {
	serveWeb(t, searxAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "web", Name: "go outliner", Type: database.TypeWeb},
		database.Node{UUID: "hit", Name: "go outliner in the database"},
	)
	q := m.tree.byUUID["web"]

	pumpWeb(t, m, runWebNode(m, q))

	web := webKids(q)
	if len(web) != 10 {
		t.Fatalf("got %d web rows, want the first 10 hits", len(web))
	}
	if web[0].typ != database.TypeWebResult {
		t.Errorf("first web row = %+v", web[0])
	}
	if c, ok := webLinkChip(m, web[0]); !ok || c.Value != "https://a.example/page" || c.Label != "Result a" {
		t.Errorf("web row must be a link chip to its result: %+v", c)
	}
	// a web node never reconciles outline mirrors
	if mirrorSources(q) != nil {
		t.Fatalf("web node produced outline mirrors: %v", mirrorSources(q))
	}
	if !strings.Contains(m.flash, "10 results") {
		t.Errorf("flash = %q", m.flash)
	}
	if m.webUpdatedAt(q.uuid) == 0 {
		t.Error("a finished web run must stamp webRunAt")
	}
}

// A re-run replaces the rows the last run made and leaves a note filed under
// the search alone.
func TestWebNodeRerunReplacesOnlyItsRows(t *testing.T) {
	serveWeb(t, searxAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "web", Name: "go outliner", Type: database.TypeWeb},
	)
	q := m.tree.byUUID["web"]
	note := &item{uuid: "mine", name: "read these on the train", parent: q}
	q.children = []*item{note}
	m.tree.byUUID[note.uuid] = note

	pumpWeb(t, m, runWebNode(m, q))
	pumpWeb(t, m, runWebNode(m, q))

	if len(q.children) != 11 {
		t.Fatalf("got %d children, want 10 fresh hits plus the user's row", len(q.children))
	}
	if q.children[10] != note {
		t.Errorf("the user's own child must survive a re-run")
	}
	for _, c := range q.children[:10] {
		if c.typ != database.TypeWebResult {
			t.Errorf("row %q is not a generated result", c.name)
		}
	}
}

// The /settings searxng.url field names the instance with the most explicit
// priority: a run points at it even with no credentials file.
func TestWebNodeUsesTheSearxngURLSetting(t *testing.T) {
	prev := wsClient
	wsClient = websearch.Client{}
	t.Cleanup(func() { wsClient = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searxAnswer()))
	}))
	t.Cleanup(srv.Close)

	m, _ := dbModel(t,
		database.Node{UUID: "web", Name: "go outliner", Type: database.TypeWeb},
	)
	m.ctx.Paths.Config = t.TempDir() // no credentials.json
	m.settings = map[string]string{"searxng.url": srv.URL}

	q := m.tree.byUUID["web"]
	pumpWeb(t, m, runWebNode(m, q))

	if len(webKids(q)) != 10 {
		t.Fatalf("got %d web rows, want the 10 hits the setting's instance returned", len(webKids(q)))
	}
}

// With no instance configured, a web run errors and keeps whatever rows the
// last good run produced.
func TestWebNodeWithoutAnInstanceErrors(t *testing.T) {
	prev := wsClient
	wsClient = websearch.Client{}
	t.Cleanup(func() { wsClient = prev })

	m, _ := dbModel(t,
		database.Node{UUID: "web", Name: "go outliner", Type: database.TypeWeb},
	)
	m.ctx.Paths.Config = t.TempDir() // no credentials.json

	q := m.tree.byUUID["web"]
	pumpWeb(t, m, runWebNode(m, q))

	if !strings.Contains(m.flash, "searxng URL missing") {
		t.Fatalf("flash = %q, want the missing-url message", m.flash)
	}
	if !m.flashErr {
		t.Error("a failed web run must flag the flash as an error (red)")
	}
	if len(webKids(q)) != 0 {
		t.Errorf("a failed run must hang no rows")
	}
}

// A web run against a node that is no longer a web node (deleted or retyped
// mid-run) drops the rows instead of writing to the wrong node.
func TestWebNodeStaleNodeDropsRows(t *testing.T) {
	serveWeb(t, searxAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "web", Name: "go outliner", Type: database.TypeWeb},
	)
	q := m.tree.byUUID["web"]
	cmd := runWebNode(m, q)
	q.typ = database.TypeBullets // retyped while the search was in flight
	msg := cmd()
	wd := msg.(webDoneMsg)
	m.handleWebDone(wd)
	if len(webKids(q)) != 0 {
		t.Errorf("a retyped node must not adopt the finished web rows")
	}
}

func webKids(q *item) []*item {
	var out []*item
	for _, c := range q.children {
		if c.typ == database.TypeWebResult {
			out = append(out, c)
		}
	}
	return out
}

// webLinkChip returns the link chip a web row's name anchors, if any.
func webLinkChip(m *Model, it *item) (database.Chip, bool) {
	for _, sp := range anchorSpans([]rune(it.name)) {
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindLink {
			return c, true
		}
	}
	return database.Chip{}, false
}
