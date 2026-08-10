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

// archiveAnswer is an advancedsearch.php output=json answer with twelve items,
// so the tests also prove an archive run keeps only the first ten.
func archiveAnswer() string {
	var b strings.Builder
	b.WriteString(`{"response":{"numFound":12,"docs":[`)
	for i := 0; i < 12; i++ {
		n := string(rune('a' + i))
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"identifier":"item-` + n + `","title":"Item ` + n + `","creator":"Someone ` + n + `"}`)
	}
	b.WriteString(`]}}`)
	return b.String()
}

// serveArchive points the shared client at a local archive.org stand-in.
func serveArchive(t *testing.T, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, "nope", status)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := arClient
	arClient = integrations.ArchiveClient{Endpoint: srv.URL}
	t.Cleanup(func() { arClient = prev })
}

// pumpArchive drives an archive node's alt+r to completion the way the editor's
// update loop does: run the command, land its archiveDoneMsg.
func pumpArchive(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	ad, ok := msg.(archiveDoneMsg)
	if !ok {
		t.Fatalf("run command returned %T, want archiveDoneMsg", msg)
	}
	m.handleArchiveDone(ad)
}

// An archive node searches archive.org's items: alt+r hangs the first ten hits
// as link-chip rows pointing at their /details/ pages, and nothing else.
func TestArchiveNodeHangsHits(t *testing.T) {
	serveArchive(t, archiveAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "arc", Name: "vonnegut", Type: database.TypeArchive},
		database.Node{UUID: "note", Name: "vonnegut in the outline"},
	)
	q := m.tree.byUUID["arc"]

	pumpArchive(t, m, runArchiveNode(m, q))

	hits := archiveKids(q)
	if len(hits) != 10 {
		t.Fatalf("got %d archive rows, want the first 10 hits", len(hits))
	}
	if hits[0].typ != database.TypeArchiveResult {
		t.Errorf("first archive row = %+v", hits[0])
	}
	if c, ok := webLinkChip(m, hits[0]); !ok || c.Value != "https://archive.org/details/item-a" || c.Label != "Item a" {
		t.Errorf("archive row must be a link chip to its item page: %+v", c)
	}
	// an archive node never reconciles outline mirrors
	if mirrorSources(q) != nil {
		t.Fatalf("archive node produced outline mirrors: %v", mirrorSources(q))
	}
	if !strings.Contains(m.flash, "archive · 10 results") {
		t.Errorf("flash = %q", m.flash)
	}
	if m.archiveUpdatedAt(q.uuid) == 0 {
		t.Error("a finished archive run must stamp archiveRunAt")
	}
	if archiveResultCount(q) != 10 {
		t.Errorf("archiveResultCount = %d", archiveResultCount(q))
	}
}

// A re-run replaces the rows the last run made and leaves a note filed under
// the search alone.
func TestArchiveNodeRerunReplacesOnlyItsRows(t *testing.T) {
	serveArchive(t, archiveAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "arc", Name: "vonnegut", Type: database.TypeArchive},
	)
	q := m.tree.byUUID["arc"]
	note := &item{uuid: "mine", name: "borrow these from the library instead", parent: q}
	q.children = []*item{note}
	m.tree.byUUID[note.uuid] = note

	pumpArchive(t, m, runArchiveNode(m, q))
	pumpArchive(t, m, runArchiveNode(m, q))

	if len(q.children) != 11 {
		t.Fatalf("got %d children, want 10 fresh hits plus the user's row", len(q.children))
	}
	if q.children[10] != note {
		t.Errorf("the user's own child must survive a re-run")
	}
	for _, c := range q.children[:10] {
		if c.typ != database.TypeArchiveResult {
			t.Errorf("row %q is not a generated result", c.name)
		}
	}
}

// A web node's rows and an archive node's rows are separate populations: an
// archive run replaces only its OWN type, so a node that once searched the web
// keeps those hits until the user clears them.
func TestArchiveRunLeavesWebRowsAlone(t *testing.T) {
	serveArchive(t, archiveAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "arc", Name: "vonnegut", Type: database.TypeArchive},
	)
	q := m.tree.byUUID["arc"]
	old := &item{uuid: "webhit", name: "an older web hit", typ: database.TypeWebResult, parent: q}
	q.children = []*item{old}
	m.tree.byUUID[old.uuid] = old

	pumpArchive(t, m, runArchiveNode(m, q))

	if len(webKids(q)) != 1 {
		t.Errorf("an archive run must not drop the web rows: %d left", len(webKids(q)))
	}
}

// A failed run keeps whatever rows the last good run produced and says why in
// red.
func TestArchiveNodeFailureKeepsRows(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "arc", Name: "vonnegut", Type: database.TypeArchive},
	)
	q := m.tree.byUUID["arc"]

	serveArchive(t, archiveAnswer(), http.StatusOK)
	pumpArchive(t, m, runArchiveNode(m, q))
	serveArchive(t, "", http.StatusServiceUnavailable)
	pumpArchive(t, m, runArchiveNode(m, q))

	if !m.flashErr {
		t.Error("a failed archive run must flag the flash as an error (red)")
	}
	if len(archiveKids(q)) != 10 {
		t.Errorf("a failed run must keep the previous rows, got %d", len(archiveKids(q)))
	}
}

// An archive run against a node that is no longer an archive node (deleted or
// retyped mid-run) drops the rows instead of writing to the wrong node.
func TestArchiveNodeStaleNodeDropsRows(t *testing.T) {
	serveArchive(t, archiveAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "arc", Name: "vonnegut", Type: database.TypeArchive},
	)
	q := m.tree.byUUID["arc"]
	cmd := runArchiveNode(m, q)
	q.typ = database.TypeBullets // retyped while the search was in flight
	m.handleArchiveDone(cmd().(archiveDoneMsg))
	if len(archiveKids(q)) != 0 {
		t.Errorf("a retyped node must not adopt the finished archive rows")
	}
}

func archiveKids(q *item) []*item {
	var out []*item
	for _, c := range q.children {
		if c.typ == database.TypeArchiveResult {
			out = append(out, c)
		}
	}
	return out
}
