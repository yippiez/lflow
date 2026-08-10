// An archive-search node: the web node's sibling, pointed at archive.org. The
// two share a shape — the name is the search, alt+r runs it, the hits hang under
// it as REAL child nodes — and differ only in where the words go: the web node
// asks the user's SearxNG instance about the live web, this one asks
// archive.org's advanced search about the archive's own ITEMS (scanned books,
// recordings, films, software), NOT the Wayback Machine's snapshots of a URL.
// The first archiveResultLimit hits become TypeArchiveResult rows — a link chip
// whose label is the item's title and whose target is its /details/ page.
// Re-running replaces the rows the last run made and touches nothing else, so a
// note filed under the search, or a hit moved out from under it, survives.

package editor

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/integrations"
)

// archiveResultLimit is how many hits a run keeps — the first page's top ten.
const archiveResultLimit = integrations.DefaultLimit

// archiveRunAtKey is where a finished run's timestamp is remembered.
const archiveRunAtKey = "archiveRunAt"

func init() {
	registerType(nodeType{
		key:            database.TypeArchive,
		label:          "Archive Search",
		inlineEditable: true,
		disableChips:   true,
		prefix:         archivePrefix,
		run:            runArchiveNode,
	})
}

// archivePrefix mirrors the web node's ⌕ but tinted yellow, so the three search
// nodes read as siblings — the same shape in three domains: the outline (dim),
// the web (cyan), the archive (yellow).
func archivePrefix(*item) string { return cYellow + "⌕" + cReset + " " }

// archiveSearchTimeout bounds one archive run.
const archiveSearchTimeout = 25 * time.Second

// arClient is the shared archive.org backend; tests point it at a local server.
// Unlike the web node's there is nothing to configure here — archive.org is one
// public host — so a run works out of the box.
var arClient integrations.ArchiveClient

// archiveDoneMsg lands a finished archive run back on the UI goroutine. The
// uuid is checked against the live node so a node deleted mid-run just drops
// the rows.
type archiveDoneMsg struct {
	uuid string
	rows []searchRow
	err  string
}

// runArchiveNode is the archive node's alt+r: start one archive.org search. The
// network call runs off the UI goroutine and delivers archiveDoneMsg.
func runArchiveNode(_ *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	term := it.name
	return func() tea.Msg {
		c := arClient // a per-run copy: the shared client is read from a worker
		ctx, cancel := context.WithTimeout(context.Background(), archiveSearchTimeout)
		defer cancel()
		res, err := c.Search(ctx, term, archiveResultLimit)
		rows := make([]searchRow, 0, len(res))
		for _, r := range res {
			rows = append(rows, searchRow{text: r.Title, url: r.URL})
		}
		out := archiveDoneMsg{uuid: it.uuid, rows: rows}
		if err != nil {
			out.err = err.Error()
		}
		return out
	}
}

// handleArchiveDone lands a finished archive run: swap the node's generated rows
// and remember when it ran. A failure keeps the previous rows (stale beats
// empty) and flashes the reason.
func (m *Model) handleArchiveDone(msg archiveDoneMsg) {
	it := m.tree.byUUID[msg.uuid]
	if it == nil || it.typ != database.TypeArchive {
		return // node deleted or retyped mid-run — drop the rows
	}
	if msg.err != "" {
		m.errorFlash(msg.err)
		return
	}
	n := m.setSearchRows(it, msg.rows, database.TypeArchiveResult)
	m.nodeStore(msg.uuid)[archiveRunAtKey] = time.Now().Unix()
	m.flash = fmt.Sprintf("archive · %d results", n)
	m.unsaved = true
}

// archiveUpdatedAt is the unix-seconds of an archive node's last run (0 if
// never).
func (m *Model) archiveUpdatedAt(uuid string) int64 { return m.searchRunAt(uuid, archiveRunAtKey) }

// archiveResultCount counts the archive rows hanging under an archive node.
func archiveResultCount(q *item) int { return searchResultCount(q, database.TypeArchiveResult) }
