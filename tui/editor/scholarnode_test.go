package editor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/integrations"
)

// scholarAnswer is a google_scholar format=json answer with twelve papers, so
// the tests also prove a scholar run keeps only the first ten. Every paper
// carries a full citation; the ragged shapes engines really send are the
// parser's business (see integrations/scholar_test.go).
func scholarAnswer() string {
	var b strings.Builder
	b.WriteString(`{"query":"outliner","results":[`)
	for i := 0; i < 12; i++ {
		n := string(rune('a' + i))
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"url":"https://` + n + `.example/paper","title":"Paper ` + n + `",` +
			`"content":"about ` + n + `","authors":["` + n + ` Author"],` +
			`"journal":"Journal of ` + n + `","publishedDate":"2019-01-01T00:00:00"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// serveScholar points the shared client at a local SearxNG stand-in.
func serveScholar(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := wsClient
	wsClient = integrations.Client{Instance: srv.URL}
	t.Cleanup(func() { wsClient = prev })
}

// pumpScholar drives a scholar node's alt+r to completion the way the editor's
// update loop does: run the command, land its scholarDoneMsg.
func pumpScholar(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	sd, ok := msg.(scholarDoneMsg)
	if !ok {
		t.Fatalf("run command returned %T, want scholarDoneMsg", msg)
	}
	m.handleScholarDone(sd)
}

// A scholar node searches the LITERATURE: alt+r hangs the first ten papers as
// rows whose link chip is the title and whose tail is the citation.
func TestScholarNodeHangsPapers(t *testing.T) {
	serveScholar(t, scholarAnswer())
	m, _ := dbModel(t,
		database.Node{UUID: "sch", Name: "outliner", Type: database.TypeScholar},
	)
	q := m.tree.byUUID["sch"]

	pumpScholar(t, m, runScholarNode(m, q))

	papers := scholarKids(q)
	if len(papers) != 10 {
		t.Fatalf("got %d paper rows, want the first 10 hits", len(papers))
	}
	if papers[0].typ != database.TypeScholarResult {
		t.Errorf("first paper row = %+v", papers[0])
	}
	c, ok := webLinkChip(m, papers[0])
	if !ok || c.Value != "https://a.example/paper" || c.Label != "Paper a" {
		t.Errorf("a paper row must be a link chip to the paper, labeled with its title: %+v", c)
	}
	if !strings.HasSuffix(papers[0].name, " · a Author · Journal of a · 2019") {
		t.Errorf("row name = %q, want the citation trailing the chip", papers[0].name)
	}
	// a scholar node never reconciles outline mirrors
	if mirrorSources(q) != nil {
		t.Fatalf("scholar node produced outline mirrors: %v", mirrorSources(q))
	}
	if !strings.Contains(m.flash, "10 papers") {
		t.Errorf("flash = %q", m.flash)
	}
	if m.scholarUpdatedAt(q.uuid) == 0 {
		t.Error("a finished scholar run must stamp scholarRunAt")
	}
}

// The citation reads quiet behind the title: the muted tail starts at the
// separator the row was built with, never inside the chip anchor.
func TestScholarRowMutesItsCitation(t *testing.T) {
	serveScholar(t, scholarAnswer())
	m, _ := dbModel(t,
		database.Node{UUID: "sch", Name: "outliner", Type: database.TypeScholar},
	)
	q := m.tree.byUUID["sch"]
	pumpScholar(t, m, runScholarNode(m, q))

	row := scholarKids(q)[0]
	d := scholarMuteFrom(row.name)
	if d < 0 {
		t.Fatalf("no muted tail in %q", row.name)
	}
	runes := []rune(row.name)
	if string(runes[d:]) != " · a Author · Journal of a · 2019" {
		t.Errorf("muted tail = %q", string(runes[d:]))
	}
	// the anchor must be intact ahead of the tail, or the row loses its link
	if spans := anchorSpans(runes); len(spans) != 1 || spans[0].end > d {
		t.Errorf("the chip anchor must sit whole before the tail: %+v", spans)
	}
	if scholarMuteFrom("a title with no citation") != -1 {
		t.Error("a row without a citation mutes nothing")
	}
}

// A paper with no citation fields is its title alone — no dangling separator.
func TestScholarRowWithoutACitation(t *testing.T) {
	serveScholar(t, `{"results":[{"url":"https://x.example/p","title":"Bare paper"}]}`)
	m, _ := dbModel(t,
		database.Node{UUID: "sch", Name: "outliner", Type: database.TypeScholar},
	)
	q := m.tree.byUUID["sch"]
	pumpScholar(t, m, runScholarNode(m, q))

	papers := scholarKids(q)
	if len(papers) != 1 {
		t.Fatalf("got %d paper rows, want 1", len(papers))
	}
	if strings.Contains(papers[0].name, scholarCiteSep) {
		t.Errorf("row name = %q, want the chip alone", papers[0].name)
	}
}

// A re-run replaces the rows the last run made and leaves a note filed under
// the search alone.
func TestScholarNodeRerunReplacesOnlyItsRows(t *testing.T) {
	serveScholar(t, scholarAnswer())
	m, _ := dbModel(t,
		database.Node{UUID: "sch", Name: "outliner", Type: database.TypeScholar},
	)
	q := m.tree.byUUID["sch"]
	note := &item{uuid: "mine", name: "read these before the seminar", parent: q}
	q.children = []*item{note}
	m.tree.byUUID[note.uuid] = note

	pumpScholar(t, m, runScholarNode(m, q))
	pumpScholar(t, m, runScholarNode(m, q))

	if len(q.children) != 11 {
		t.Fatalf("got %d children, want 10 fresh papers plus the user's row", len(q.children))
	}
	if q.children[10] != note {
		t.Errorf("the user's own child must survive a re-run")
	}
	for _, c := range q.children[:10] {
		if c.typ != database.TypeScholarResult {
			t.Errorf("row %q is not a generated result", c.name)
		}
	}
}

// The /settings searxng.url field names the instance for a scholar run exactly
// as it does for a web one — one instance, both searches.
func TestScholarNodeUsesTheSearxngURLSetting(t *testing.T) {
	prev := wsClient
	wsClient = integrations.Client{}
	t.Cleanup(func() { wsClient = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(scholarAnswer()))
	}))
	t.Cleanup(srv.Close)

	m, _ := dbModel(t,
		database.Node{UUID: "sch", Name: "outliner", Type: database.TypeScholar},
	)
	m.ctx.Paths.Config = t.TempDir() // no credentials.json
	m.settings = map[string]string{"searxng.url": srv.URL}

	q := m.tree.byUUID["sch"]
	pumpScholar(t, m, runScholarNode(m, q))

	if len(scholarKids(q)) != 10 {
		t.Fatalf("got %d paper rows, want the 10 the setting's instance returned", len(scholarKids(q)))
	}
}

// With no instance configured, a scholar run errors and hangs nothing.
func TestScholarNodeWithoutAnInstanceErrors(t *testing.T) {
	prev := wsClient
	wsClient = integrations.Client{}
	t.Cleanup(func() { wsClient = prev })

	m, _ := dbModel(t,
		database.Node{UUID: "sch", Name: "outliner", Type: database.TypeScholar},
	)
	m.ctx.Paths.Config = t.TempDir() // no credentials.json

	q := m.tree.byUUID["sch"]
	pumpScholar(t, m, runScholarNode(m, q))

	if !strings.Contains(m.flash, "searxng URL missing") {
		t.Fatalf("flash = %q, want the missing-url message", m.flash)
	}
	if !m.flashErr {
		t.Error("a failed scholar run must flag the flash as an error (red)")
	}
	if len(scholarKids(q)) != 0 {
		t.Errorf("a failed run must hang no rows")
	}
}

// A scholar run against a node that is no longer a scholar node (deleted or
// retyped mid-run) drops the rows instead of writing to the wrong node.
func TestScholarNodeStaleNodeDropsRows(t *testing.T) {
	serveScholar(t, scholarAnswer())
	m, _ := dbModel(t,
		database.Node{UUID: "sch", Name: "outliner", Type: database.TypeScholar},
	)
	q := m.tree.byUUID["sch"]
	cmd := runScholarNode(m, q)
	q.typ = database.TypeBullets // retyped while the search was in flight
	sd := cmd().(scholarDoneMsg)
	m.handleScholarDone(sd)
	if len(scholarKids(q)) != 0 {
		t.Errorf("a retyped node must not adopt the finished paper rows")
	}
}

func scholarKids(q *item) []*item {
	var out []*item
	for _, c := range q.children {
		if c.typ == database.TypeScholarResult {
			out = append(out, c)
		}
	}
	return out
}
