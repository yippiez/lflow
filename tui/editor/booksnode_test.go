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

// booksAnswer is a Google Books volumes answer with twelve volumes, so the
// tests also prove a run keeps only the first ten.
func booksAnswer() string {
	var b strings.Builder
	b.WriteString(`{"totalItems":12,"items":[`)
	for i := 0; i < 12; i++ {
		n := string(rune('a' + i))
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"volumeInfo":{"title":"Book ` + n + `","authors":["Author ` + n + `"],` +
			`"publishedDate":"200` + string(rune('0'+i%10)) + `","publisher":"Press ` + n + `",` +
			`"canonicalVolumeLink":"https://books.google.com/books/about/` + n + `"}}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// serveBooks points the shared client at a local Google Books stand-in.
func serveBooks(t *testing.T, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, "nope", status)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := booksClient
	booksClient = integrations.BooksClient{Endpoint: srv.URL}
	t.Cleanup(func() { booksClient = prev })
}

// pumpBooks drives a books node's alt+r to completion the way the editor's
// update loop does: run the command, land its booksDoneMsg.
func pumpBooks(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	bd, ok := msg.(booksDoneMsg)
	if !ok {
		t.Fatalf("run command returned %T, want booksDoneMsg", msg)
	}
	m.handleBooksDone(bd)
}

// A books node searches Google Books: alt+r hangs the first ten volumes as
// rows, each a link chip to the book's page trailed by its byline and year.
func TestBooksNodeHangsVolumes(t *testing.T) {
	serveBooks(t, booksAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "books", Name: "refactoring", Type: database.TypeBooks},
	)
	q := m.tree.byUUID["books"]

	pumpBooks(t, m, runBooksNode(m, q))

	rows := bookKids(q)
	if len(rows) != 10 {
		t.Fatalf("got %d book rows, want the first 10 volumes", len(rows))
	}
	if rows[0].typ != database.TypeBookResult {
		t.Errorf("first book row = %+v", rows[0])
	}
	c, ok := bookLinkChip(m, rows[0])
	if !ok || c.Value != "https://books.google.com/books/about/a" || c.Label != "Book a" {
		t.Errorf("a book row must be a link chip to its volume page: %+v", c)
	}
	// the byline rides after the chip as plain text — a book list answers
	// "which edition is this" on the row itself
	if tail := database.DisplayAnchors(rows[0].name, m.chips); !strings.HasSuffix(tail, "Author a · 2000 · Press a") {
		t.Errorf("row text = %q, want the byline trailing the title", tail)
	}
	if !strings.Contains(m.flash, "10 results") {
		t.Errorf("flash = %q", m.flash)
	}
	if m.booksUpdatedAt(q.uuid) == 0 {
		t.Error("a finished book run must stamp booksRunAt")
	}
}

// A volume with no page still becomes a row: the title is plain text, and the
// byline still trails it.
func TestBooksNodeRowWithoutALink(t *testing.T) {
	serveBooks(t, `{"items":[{"volumeInfo":{"title":"Unlinked","authors":["Nobody"]}}]}`, http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "books", Name: "unlinked", Type: database.TypeBooks},
	)
	q := m.tree.byUUID["books"]

	pumpBooks(t, m, runBooksNode(m, q))

	rows := bookKids(q)
	if len(rows) != 1 {
		t.Fatalf("got %d book rows, want 1", len(rows))
	}
	if rows[0].name != "Unlinked · Nobody" {
		t.Errorf("row text = %q", rows[0].name)
	}
	if _, ok := bookLinkChip(m, rows[0]); ok {
		t.Error("a volume with no page must hang no link chip")
	}
}

// A re-run replaces the rows the last run made and leaves a note filed under
// the search alone.
func TestBooksNodeRerunReplacesOnlyItsRows(t *testing.T) {
	serveBooks(t, booksAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "books", Name: "refactoring", Type: database.TypeBooks},
	)
	q := m.tree.byUUID["books"]
	note := &item{uuid: "mine", name: "order this one", parent: q}
	q.children = []*item{note}
	m.tree.byUUID[note.uuid] = note

	pumpBooks(t, m, runBooksNode(m, q))
	pumpBooks(t, m, runBooksNode(m, q))

	if len(q.children) != 11 {
		t.Fatalf("got %d children, want 10 fresh volumes plus the user's row", len(q.children))
	}
	if q.children[10] != note {
		t.Errorf("the user's own child must survive a re-run")
	}
	for _, c := range q.children[:10] {
		if c.typ != database.TypeBookResult {
			t.Errorf("row %q is not a generated result", c.name)
		}
	}
}

// A failed run keeps whatever rows the last good run produced — stale beats
// empty — and says why in red.
func TestBooksNodeFailedRunKeepsTheOldRows(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "books", Name: "refactoring", Type: database.TypeBooks},
	)
	q := m.tree.byUUID["books"]

	serveBooks(t, booksAnswer(), http.StatusOK)
	pumpBooks(t, m, runBooksNode(m, q))
	if len(bookKids(q)) != 10 {
		t.Fatalf("setup: want 10 rows from the good run")
	}

	serveBooks(t, "", http.StatusTooManyRequests)
	pumpBooks(t, m, runBooksNode(m, q))

	if len(bookKids(q)) != 10 {
		t.Errorf("a failed run must keep the previous rows, got %d", len(bookKids(q)))
	}
	if !m.flashErr {
		t.Error("a failed book run must flag the flash as an error (red)")
	}
	if !strings.Contains(m.flash, "api_key") {
		t.Errorf("flash = %q, want the quota fix", m.flash)
	}
}

// An unnamed books node has nothing to search for, and says so instead of
// asking Google for the empty string.
func TestBooksNodeWithoutATermErrors(t *testing.T) {
	serveBooks(t, booksAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "books", Name: "   ", Type: database.TypeBooks},
	)
	q := m.tree.byUUID["books"]

	if cmd := runBooksNode(m, q); cmd != nil {
		t.Fatal("an unnamed books node must not start a search")
	}
	if !m.flashErr || !strings.Contains(m.flash, "search for") {
		t.Errorf("flash = %q (err=%v), want the name-this-node message", m.flash, m.flashErr)
	}
}

// A run against a node that is no longer a books node (deleted or retyped
// mid-run) drops the rows instead of writing to the wrong node.
func TestBooksNodeStaleNodeDropsRows(t *testing.T) {
	serveBooks(t, booksAnswer(), http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "books", Name: "refactoring", Type: database.TypeBooks},
	)
	q := m.tree.byUUID["books"]
	cmd := runBooksNode(m, q)
	q.typ = database.TypeBullets // retyped while the search was in flight
	m.handleBooksDone(cmd().(booksDoneMsg))
	if len(bookKids(q)) != 0 {
		t.Errorf("a retyped node must not adopt the finished book rows")
	}
}

// The row's dim suffix counts the shelf, singular and plural alike.
func TestBooksNodeSuffixCountsVolumes(t *testing.T) {
	serveBooks(t, `{"items":[{"volumeInfo":{"title":"Only One","canonicalVolumeLink":"https://books.google.com/1"}}]}`, http.StatusOK)
	m, _ := dbModel(t,
		database.Node{UUID: "books", Name: "only one", Type: database.TypeBooks},
	)
	q := m.tree.byUUID["books"]
	pumpBooks(t, m, runBooksNode(m, q))

	suffix := m.typeSuffix(row{it: q})
	if !strings.Contains(suffix, "1 book ·") || strings.Contains(suffix, "1 books") {
		t.Errorf("suffix = %q, want a singular book count", suffix)
	}
	if !strings.Contains(suffix, "updated") {
		t.Errorf("suffix = %q, want the run's age", suffix)
	}
}

func bookKids(q *item) []*item {
	var out []*item
	for _, c := range q.children {
		if c.typ == database.TypeBookResult {
			out = append(out, c)
		}
	}
	return out
}

// bookLinkChip returns the link chip a book row's name anchors, if any.
func bookLinkChip(m *Model, it *item) (database.Chip, bool) {
	for _, sp := range anchorSpans([]rune(it.name)) {
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindLink {
			return c, true
		}
	}
	return database.Chip{}, false
}
