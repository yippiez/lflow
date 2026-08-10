// A Google Books node: the bibliographic sibling of the web node. The two share
// a shape — the name is the search, alt+r runs it, the hits hang under it as
// REAL child nodes — but a books node searches BOOKS, so a row says what a book
// row has to say: a link chip to the volume's page labelled with its title, and
// after it the byline and year in plain text, because "which edition is this"
// is the question a list of books is always answering.
//
// Re-running replaces the rows the last run made and touches nothing else, so a
// note filed under the search, or a volume moved out from under it, survives.

package editor

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/integrations"
)

// bookResultLimit is how many volumes a run keeps — one screenful, like the web
// node's page of hits.
const bookResultLimit = integrations.BooksLimit

func init() {
	registerType(nodeType{
		key:            database.TypeBooks,
		label:          "Google Books",
		inlineEditable: true,
		disableChips:   true,
		prefix:         booksPrefix,
		run:            runBooksNode,
	})
}

// booksPrefix is the web node's ⌕ tinted yellow: the same search shape, a
// different shelf.
func booksPrefix(*item) string { return cYellow + "⌕" + cReset + " " }

// booksTimeout bounds one book search.
const booksTimeout = 25 * time.Second

// booksClient is the shared Google Books backend; tests point it at a local
// server.
var booksClient integrations.BooksClient

// bookRow is one generated volume row: the title the link chip wears, the
// volume's page, and the plain byline that trails them.
type bookRow struct {
	title, url, tail string
}

// booksDoneMsg lands a finished book search back on the UI goroutine. The uuid
// is checked against the live node so a node deleted mid-run just drops the rows.
type booksDoneMsg struct {
	uuid string
	rows []bookRow
	err  string
}

// runBooksNode is the books node's alt+r: start one Google Books search. The
// network call runs off the UI goroutine and delivers booksDoneMsg.
func runBooksNode(m *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	term := strings.TrimSpace(it.name)
	if term == "" {
		m.errorFlash("Books · name this node with what to search for")
		return nil
	}
	cfgDir := m.ctx.Paths.Config
	uuid := it.uuid
	return func() tea.Msg {
		// a per-run copy: ConfigDir only locates the optional quota key, so a
		// client a test already pointed somewhere keeps its endpoint
		c := booksClient
		c.ConfigDir = cfgDir
		ctx, cancel := context.WithTimeout(context.Background(), booksTimeout)
		defer cancel()
		books, err := c.Search(ctx, term, bookResultLimit)
		rows := make([]bookRow, 0, len(books))
		for _, b := range books {
			rows = append(rows, bookRow{title: bookTitle(b), url: b.Link, tail: bookTail(b)})
		}
		out := booksDoneMsg{uuid: uuid, rows: rows}
		if err != nil {
			out.err = err.Error()
		}
		return out
	}
}

// bookTitle is what the link chip is labelled with: the title, and the subtitle
// after it when the volume has one — a book whose title is "Refactoring" and
// whose subtitle is "Improving the Design of Existing Code" is unrecognizable
// without it.
func bookTitle(b integrations.Book) string {
	if b.Subtitle == "" {
		return b.Title
	}
	return b.Title + ": " + b.Subtitle
}

// bookTail is the plain text after the chip — who wrote it and when, then the
// publisher, each part dropped when the volume does not carry it.
func bookTail(b integrations.Book) string {
	var parts []string
	if by := b.Byline(); by != "" {
		parts = append(parts, by)
	}
	if b.Year != "" {
		parts = append(parts, b.Year)
	}
	if b.Publisher != "" {
		parts = append(parts, b.Publisher)
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

// handleBooksDone lands a finished book search: swap the node's generated rows
// and remember when it ran. A failure keeps the previous rows (stale beats
// empty) and flashes the reason.
func (m *Model) handleBooksDone(msg booksDoneMsg) {
	it := m.tree.byUUID[msg.uuid]
	if it == nil || it.typ != database.TypeBooks {
		return // node deleted or retyped mid-run — drop the rows
	}
	if msg.err != "" {
		m.errorFlash(msg.err)
		return
	}
	n := m.setBookRows(it, msg.rows)
	m.nodeStore(msg.uuid)["booksRunAt"] = time.Now().Unix()
	m.flash = fmt.Sprintf("books · %d results", n)
	m.unsaved = true
}

// setBookRows swaps a books node's children of TypeBookResult for one row per
// volume, leaving everything else — a note filed under the search, a book the
// user moved out to keep — alone.
func (m *Model) setBookRows(q *item, rows []bookRow) int {
	var kept []*item
	for _, c := range q.children {
		if c.typ == database.TypeBookResult {
			m.dropGeneratedRow(c)
			continue
		}
		kept = append(kept, c)
	}
	made := make([]*item, 0, len(rows))
	for _, r := range rows {
		c, err := m.tree.newItem()
		if err != nil {
			break
		}
		c.parent = q
		c.typ = database.TypeBookResult
		c.name = r.title
		if r.url != "" {
			c.name = m.createLabeledChip(chipKindLink, r.url, r.title)
		}
		c.name += r.tail
		made = append(made, c)
	}
	q.children = append(made, kept...)
	m.unsaved = true
	m.refreshRows()
	return len(made)
}

// booksUpdatedAt is the unix-seconds of a books node's last run (0 if never).
func (m *Model) booksUpdatedAt(uuid string) int64 {
	v, _ := m.nodeStore(uuid)["booksRunAt"].(int64)
	return v
}

// bookResultCount counts the volume rows hanging under a books node (direct
// children of TypeBookResult).
func bookResultCount(q *item) int {
	n := 0
	for _, c := range q.children {
		if c.typ == database.TypeBookResult {
			n++
		}
	}
	return n
}
