package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
	"github.com/lflow/lflow/pkg/tui/websearch"
)

// The websearch node: an outline row whose text IS a web search. alt+r asks the
// user's SearxNG instance (see pkg/tui/websearch — the only backend, and no
// instance means an error rather than a query sent somewhere they did not
// choose) and hangs the first ten hits under the node AS REAL CHILD NODES, one
// per hit: a link row whose text is the title and whose target is the result.
// They are ordinary outline nodes — fold them, star them, note them, indent
// something under one, or move one out to keep it — and the next run replaces
// the ones still sitting under the node.
//
// The node itself keeps one band: a dim "Last Updated: …" chip, and while a run
// is in flight the shining "searching…" line. The stamp lives in node_output
// (local, never synced) so the chip still reads right after a restart, while
// the hits themselves are just outline.

const (
	wsLimit   = websearch.DefaultLimit // the first ten results
	wsTimeout = 25 * time.Second
)

// wsClient is the shared backend; tests point it at a local server.
var wsClient websearch.Client

// wsGlyph is the node's mark: ⊕, the astronomical symbol for Earth — a globe
// drawn in the plain Unicode the outline is made of (no emoji, house rule), and
// nothing like the query node's ⌕ magnifier: one searches the web, the other
// searches your own outline.
func wsGlyph() (string, string) { return "⊕", editor.NodeTheme().Cyan }

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeWebSearch, Label: "Web Search",
		InlineEditable: true, // the row is the query — edit it inline
		Glyph:          wsGlyph,
		Run:            runWebSearch,
		Preview:        wsPreview,
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			return "websearch", "", "" // the hits are children now; they carry themselves
		},
		OnRemove: func(h editor.NodeHost, uuid string) {
			if st := wsStateOf(h, uuid); st.cancel != nil {
				st.cancel()
				st.cancel, st.busy = nil, false
			}
			delete(h.NodeStore(uuid), "animating")
		},
	})
	// the hit rows themselves: generated, so kept out of the /type picker. They
	// are not inline-editable (a run replaces them), but they are otherwise a
	// plain node — the link chip in the name does all the rendering.
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeWebResult, Label: "Web Result",
		Internal:       true,
		InlineEditable: false,
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			return "result", "", ""
		},
	})
}

// wsState is one node's in-flight state (NodeStore, key "websearch"). Only the
// run itself lives here — the results are outline nodes, and the stamp is in
// node_output.
type wsState struct {
	busy   bool
	cancel func()
	errMsg string
	at     int64 // unix seconds of the last finished run; 0 = never (or not loaded)
	loaded bool
}

func wsStateOf(h editor.NodeHost, uuid string) *wsState {
	d := h.NodeStore(uuid)
	st, _ := d["websearch"].(*wsState)
	if st == nil {
		st = &wsState{}
		d["websearch"] = st
	}
	if !st.loaded {
		st.loaded = true
		st.at = wsLoadStamp(h, uuid)
	}
	return st
}

// wsStamp is the persisted piece: when this node last ran. It rides in
// node_output, which is local and never synced — the hits are the synced part.
type wsStampData struct {
	At int64 `json:"at"`
}

func wsLoadStamp(h editor.NodeHost, uuid string) int64 {
	db := h.NodeDB()
	if db == nil {
		return 0
	}
	raw, err := database.LoadNodeOutput(db, uuid)
	if err != nil || raw == "" {
		return 0
	}
	var d wsStampData
	if json.Unmarshal([]byte(raw), &d) != nil {
		return 0
	}
	return d.At
}

func wsSaveStamp(h editor.NodeHost, uuid string, at int64) {
	db := h.NodeDB()
	if db == nil {
		return
	}
	if raw, err := json.Marshal(wsStampData{At: at}); err == nil {
		_ = database.SaveNodeOutput(db, uuid, string(raw))
	}
}

// wsDoneMsg lands a finished search back in the update loop.
type wsDoneMsg struct {
	uuid    string
	node    editor.NodeRef
	results []websearch.Result
	err     string
}

// runWebSearch (alt+r) searches the user's instance for the node's text. Never
// auto-runs.
func runWebSearch(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	uuid := n.UUID()
	st := wsStateOf(h, uuid)
	if st.busy {
		h.NodeFlash("websearch · already searching")
		return nil
	}
	query := strings.TrimSpace(n.Text())
	if query == "" {
		h.NodeFlash("websearch · type what to search first")
		return nil
	}
	// re-read each run, so naming an instance in credentials.json takes effect
	// without a restart
	wsClient.ConfigDir = h.NodeConfigDir()
	ctx, cancel := context.WithTimeout(context.Background(), wsTimeout)
	st.busy, st.cancel, st.errMsg = true, cancel, ""
	h.NodeStore(uuid)["animating"] = true // keeps the shine ticking while it runs
	return func() tea.Msg {
		defer cancel()
		res, err := wsClient.Search(ctx, query, wsLimit)
		msg := wsDoneMsg{uuid: uuid, node: n, results: res}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}

// HandleNodePlugin lands a finished search: the hits become the node's children
// (editor.NodePluginMsg). A failed run keeps the rows the last one produced —
// stale results beat no results, and the band says how old they are.
func (msg wsDoneMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	st := wsStateOf(h, msg.uuid)
	st.busy, st.cancel = false, nil
	delete(h.NodeStore(msg.uuid), "animating")
	st.errMsg = msg.err
	if msg.err != "" {
		h.NodeFlash("websearch · " + msg.err)
		return nil
	}

	rows := make([]editor.NodeRow, 0, len(msg.results))
	for _, r := range msg.results {
		rows = append(rows, editor.NodeRow{Text: r.Title, URL: r.URL})
	}
	n := h.NodeSetGenerated(msg.node, database.TypeWebResult, rows)
	st.at = time.Now().Unix()
	wsSaveStamp(h, msg.uuid, st.at)
	h.NodeFlash(fmt.Sprintf("websearch · %d results", n))
	return nil
}

// wsPreview is the node's one band: the time chip, or what the run is doing.
// The results are rows of their own beneath it.
func wsPreview(h editor.NodeHost, n editor.NodeRef, rail string, maxLine int, focused bool) []string {
	th := editor.NodeTheme()
	st := wsStateOf(h, n.UUID())
	line := func(s string) string { return editor.NodeClip(rail+th.Reset+"  "+s, maxLine) }

	switch {
	case st.busy:
		return []string{line(editor.ShineText("searching…"))}
	case st.errMsg != "":
		return []string{line(th.Red + st.errMsg + th.Reset)}
	case st.at > 0:
		return []string{line(th.Dim + "Last Updated: " + editor.NodeRelTime(st.at) + th.Reset)}
	}
	return nil // never run — the row is just a line of text until ⌥r
}
