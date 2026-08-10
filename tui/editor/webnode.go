// A web-search node: the sibling of a Query node that searches the WEB instead
// of the outline. The two share a shape — the name is the search, alt+r runs
// it, the hits hang under it as REAL child nodes — but a web node has no query
// language: the whole name is the term, handed to the user's SearxNG instance
// (see tui/integrations). The first webResultLimit hits become TypeWebResult
// rows — a link chip whose label is the title and whose target is the URL.
// Re-running replaces the rows the last run made and touches nothing else, so a
// note filed under the search, or a hit moved out from under it, survives. The
// row-making itself lives in searchrows.go, shared with the archive node
// (archivenode.go) — the same shape aimed at archive.org instead of the web.

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

// webResultLimit is how many web hits a run keeps — the first page's top ten.
const webResultLimit = integrations.DefaultLimit

// webRunAtKey is where a finished run's timestamp is remembered.
const webRunAtKey = "webRunAt"

func init() {
	registerType(nodeType{
		key:            database.TypeWeb,
		label:          "Web Search",
		inlineEditable: true,
		disableChips:   true,
		prefix:         webPrefix,
		run:            runWebNode,
	})
}

// webPrefix mirrors the query node's ⌕ but tinted cyan, so the two search nodes
// read as siblings: the same shape, a different domain.
func webPrefix(*item) string { return cCyan + "⌕" + cReset + " " }

// webSearchTimeout bounds one web run.
const webSearchTimeout = 25 * time.Second

// wsClient is the shared web-search backend; tests point it at a local server.
var wsClient integrations.Client

// webDoneMsg lands a finished web run back on the UI goroutine. The uuid is
// checked against the live node so a node deleted mid-run just drops the rows.
type webDoneMsg struct {
	uuid string
	rows []searchRow
	err  string
}

// runWebNode is the web node's alt+r: start one web search. The network call
// runs off the UI goroutine and delivers webDoneMsg.
func runWebNode(m *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	term := it.name
	cfgDir := ""
	if m.ctx.Paths.Config != "" {
		cfgDir = m.ctx.Paths.Config
	}
	return func() tea.Msg {
		// a per-run copy: the /settings searxng.url field names the instance with
		// the most explicit priority (ahead of credentials.json);
		// an empty setting leaves the client's own resolution in charge
		c := wsClient
		c.ConfigDir = cfgDir
		if u := strings.TrimSpace(m.setting("searxng.url")); u != "" {
			c.Instance = u
		}
		ctx, cancel := context.WithTimeout(context.Background(), webSearchTimeout)
		defer cancel()
		res, err := c.Search(ctx, term, webResultLimit)
		rows := make([]searchRow, 0, len(res))
		for _, r := range res {
			rows = append(rows, searchRow{text: r.Title, url: r.URL})
		}
		out := webDoneMsg{uuid: it.uuid, rows: rows}
		if err != nil {
			out.err = err.Error()
		}
		return out
	}
}

// handleWebDone lands a finished web run: swap the node's generated rows and
// remember when it ran. A failure keeps the previous rows (stale beats empty)
// and flashes the reason.
func (m *Model) handleWebDone(msg webDoneMsg) {
	it := m.tree.byUUID[msg.uuid]
	if it == nil || it.typ != database.TypeWeb {
		return // node deleted or retyped mid-run — drop the rows
	}
	if msg.err != "" {
		m.errorFlash(msg.err)
		return
	}
	n := m.setSearchRows(it, msg.rows, database.TypeWebResult)
	m.nodeStore(msg.uuid)[webRunAtKey] = time.Now().Unix()
	m.flash = fmt.Sprintf("web · %d results", n)
	m.unsaved = true
}

// webUpdatedAt is the unix-seconds of a web node's last run (0 if never).
func (m *Model) webUpdatedAt(uuid string) int64 { return m.searchRunAt(uuid, webRunAtKey) }

// webResultCount counts the web rows hanging under a web node (direct children
// of TypeWebResult).
func webResultCount(q *item) int { return searchResultCount(q, database.TypeWebResult) }
