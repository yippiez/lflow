// A Google Scholar node: the web node pointed at the literature. It is the
// same shape as its sibling — the name is the search, alt+r runs it, the hits
// hang under it as REAL child nodes, a re-run replaces only the rows the last
// run made — and it reaches Scholar the same way, through the user's own
// SearxNG instance (see tui/integrations), narrowed to the google_scholar
// engine.
//
// What differs is what a hit IS. A web hit is a page, so a web row is its
// title and its link and nothing else. A scholar hit is a paper, so a
// TypeScholarResult row is the title as a link chip followed by the paper's
// citation — authors, journal, year — as a muted tail, which is what turns a
// search under a research note into something readable as a reading list.

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

// scholarResultLimit is how many papers a run keeps — the first page's top ten,
// like the web node's.
const scholarResultLimit = integrations.DefaultLimit

// scholarSearchTimeout bounds one scholar run. Scholar is slower to answer
// through a metasearch instance than the general web is, so it gets more rope
// than webSearchTimeout.
const scholarSearchTimeout = 40 * time.Second

// scholarCiteSep separates a scholar row's title from its citation. It is the
// log node's separator on purpose: scholarMuteFrom mutes from it, so the
// citation reads quiet behind the title the same way a log line's metadata does.
const scholarCiteSep = " · "

func init() {
	registerType(nodeType{
		key:            database.TypeScholar,
		label:          "Scholar Search",
		inlineEditable: true,
		disableChips:   true,
		prefix:         scholarPrefix,
		run:            runScholarNode,
	})
}

// scholarPrefix mirrors the web node's ⌕ but tinted magenta: the third search
// node, the same shape as the other two, its own domain.
func scholarPrefix(*item) string { return cMagenta + "⌕" + cReset + " " }

// scholarRow is one generated hit row: its link target, the paper's title, and
// the citation that trails it.
type scholarRow struct {
	title, url, citation string
}

// text is the row's stored name when the hit has no URL to hang a chip on.
func (r scholarRow) text() string {
	if r.citation == "" {
		return r.title
	}
	return r.title + scholarCiteSep + r.citation
}

// scholarDoneMsg lands a finished scholar run back on the UI goroutine. The
// uuid is checked against the live node so a node deleted mid-run just drops
// the rows.
type scholarDoneMsg struct {
	uuid string
	rows []scholarRow
	err  string
}

// runScholarNode is the scholar node's alt+r: start one Scholar search. The
// network call runs off the UI goroutine and delivers scholarDoneMsg.
func runScholarNode(m *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	term := it.name
	cfgDir := ""
	if m.ctx.Paths.Config != "" {
		cfgDir = m.ctx.Paths.Config
	}
	instance := strings.TrimSpace(m.setting("searxng.url"))
	return func() tea.Msg {
		// a per-run copy of the shared backend, resolved exactly like a web run's:
		// the /settings searxng.url field names the instance ahead of
		// credentials.json, and an empty setting leaves the client's own
		// resolution in charge
		c := wsClient
		c.ConfigDir = cfgDir
		if instance != "" {
			c.Instance = instance
		}
		ctx, cancel := context.WithTimeout(context.Background(), scholarSearchTimeout)
		defer cancel()
		res, err := c.SearchScholar(ctx, term, scholarResultLimit)
		rows := make([]scholarRow, 0, len(res))
		for _, r := range res {
			rows = append(rows, scholarRow{title: r.Title, url: r.URL, citation: r.Citation()})
		}
		out := scholarDoneMsg{uuid: it.uuid, rows: rows}
		if err != nil {
			out.err = err.Error()
		}
		return out
	}
}

// handleScholarDone lands a finished scholar run: swap the node's generated
// rows and remember when it ran. A failure keeps the previous rows (stale beats
// empty) and flashes the reason.
func (m *Model) handleScholarDone(msg scholarDoneMsg) {
	it := m.tree.byUUID[msg.uuid]
	if it == nil || it.typ != database.TypeScholar {
		return // node deleted or retyped mid-run — drop the rows
	}
	if msg.err != "" {
		m.errorFlash(msg.err)
		return
	}
	n := m.setScholarRows(it, msg.rows)
	m.nodeStore(msg.uuid)["scholarRunAt"] = time.Now().Unix()
	m.flash = fmt.Sprintf("scholar · %d papers", n)
	m.unsaved = true
}

// setScholarRows swaps a scholar node's children of TypeScholarResult for one
// row per paper, leaving everything else — a note filed under the search, a
// paper the user moved out to keep — alone.
func (m *Model) setScholarRows(q *item, rows []scholarRow) int {
	var kept []*item
	for _, c := range q.children {
		if c.typ == database.TypeScholarResult {
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
		c.typ = database.TypeScholarResult
		c.name = r.text()
		if r.url != "" {
			// the title becomes the chip's LABEL, so the row opens the paper while
			// still reading as its title; the citation trails the chip as plain text
			c.name = m.createLabeledChip(chipKindLink, r.url, r.title)
			if r.citation != "" {
				c.name += scholarCiteSep + r.citation
			}
		}
		made = append(made, c)
	}
	q.children = append(made, kept...)
	m.unsaved = true
	m.refreshRows()
	return len(made)
}

// scholarUpdatedAt is the unix-seconds of a scholar node's last run (0 if
// never).
func (m *Model) scholarUpdatedAt(uuid string) int64 {
	v, _ := m.nodeStore(uuid)["scholarRunAt"].(int64)
	return v
}

// scholarResultCount counts the papers hanging under a scholar node (direct
// children of TypeScholarResult).
func scholarResultCount(q *item) int {
	n := 0
	for _, c := range q.children {
		if c.typ == database.TypeScholarResult {
			n++
		}
	}
	return n
}
