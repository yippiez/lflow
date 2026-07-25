package nodes

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
	"github.com/lflow/lflow/pkg/tui/websearch"
)

// The websearch node: an outline row whose text IS a web search. alt+r asks
// DuckDuckGo's keyless endpoints (see pkg/tui/websearch — no account, no API
// key, nothing to configure) and hangs the first ten hits underneath the node
// as bands: rank, title, host. alt+e opens the same ten with their URLs and
// snippets in a scrollable view.
//
// The row stays an ordinary editable line — no Render override — so the query is
// typed, chipped and colored like any other node; everything the search produces
// lives in the bands beneath it.
//
// WARNING (invariant): the hits are RUN OUTPUT — they live only in the ephemeral
// node store, are never written to the database and never sync. Reopening the
// outline shows the query, not yesterday's web.

const (
	wsLimit   = websearch.DefaultLimit // the first ten results
	wsTimeout = 25 * time.Second
)

// wsClient is the shared backend; tests point it at a local server.
var wsClient websearch.Client

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeWebSearch, Label: "Web Search",
		InlineEditable: true, // the row is the query — edit it inline
		Glyph:          func() (string, string) { return "⌕", editor.NodeTheme().Cyan },
		Run:            runWebSearch,
		Preview:        wsPreview,
		View:           wsView{},
		ToContext:      wsToContext,
		OnRemove: func(h editor.NodeHost, uuid string) {
			if st := wsStateOf(h, uuid); st.cancel != nil {
				st.cancel()
				st.cancel, st.busy = nil, false
			}
			delete(h.NodeStore(uuid), "animating")
		},
	})
}

// wsState is one node's ephemeral search state (NodeStore, key "websearch").
type wsState struct {
	busy    bool
	cancel  func()
	query   string // the query the held results answer — not necessarily the row's text now
	results []websearch.Result
	errMsg  string
}

func wsStateOf(h editor.NodeHost, uuid string) *wsState {
	d := h.NodeStore(uuid)
	st, _ := d["websearch"].(*wsState)
	if st == nil {
		st = &wsState{}
		d["websearch"] = st
	}
	return st
}

// wsDoneMsg lands a finished search back in the update loop.
type wsDoneMsg struct {
	uuid, query string
	results     []websearch.Result
	err         string
}

// runWebSearch (alt+r) searches the web for the node's text. Never auto-runs.
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
	ctx, cancel := context.WithTimeout(context.Background(), wsTimeout)
	st.busy, st.cancel = true, cancel
	st.query, st.results, st.errMsg = query, nil, ""
	// the shine on the searching… band keeps the animation tick alive
	h.NodeStore(uuid)["animating"] = true
	return func() tea.Msg {
		defer cancel()
		res, err := wsClient.Search(ctx, query, wsLimit)
		msg := wsDoneMsg{uuid: uuid, query: query, results: res}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}

// HandleNodePlugin parks a finished search (editor.NodePluginMsg).
func (msg wsDoneMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	st := wsStateOf(h, msg.uuid)
	st.busy, st.cancel = false, nil
	delete(h.NodeStore(msg.uuid), "animating")
	st.query, st.results, st.errMsg = msg.query, msg.results, msg.err
	switch {
	case msg.err != "":
		h.NodeFlash("websearch · " + msg.err)
	case len(msg.results) == 0:
		h.NodeFlash("websearch · no results")
	default:
		h.NodeFlash(fmt.Sprintf("websearch · %d results · ⌥e opens them", len(msg.results)))
	}
	return nil
}

// wsHost is the compact source shown next to a hit on the narrow preview band —
// the domain, without the scheme or the "www." noise.
func wsHost(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "www.")
}

// wsRank right-aligns the rank in two columns so the titles line up through ten.
func wsRank(i int) string {
	return fmt.Sprintf("%2s", strconv.Itoa(i+1))
}

// wsPreview hangs the results beneath the node — one line per hit, always on
// once a search has run (the point of the type: run it, read the ten). It stays
// out of the way while the alt+e view is open, which shows the same ten in full.
func wsPreview(h editor.NodeHost, n editor.NodeRef, rail string, maxLine int, focused bool) []string {
	if focused {
		return nil
	}
	th := editor.NodeTheme()
	st := wsStateOf(h, n.UUID())
	line := func(s string) string { return editor.NodeClip(rail+th.Reset+"  "+s, maxLine) }

	switch {
	case st.busy:
		return []string{line(editor.ShineText("searching the web…"))}
	case st.errMsg != "":
		return []string{line(th.Red + "websearch · " + st.errMsg + th.Reset)}
	case len(st.results) == 0 && st.query != "":
		return []string{line(th.Dim + "no results · ⌥r searches again" + th.Reset)}
	case len(st.results) == 0:
		return nil // never run — the row is just a line of text until ⌥r
	}

	head := fmt.Sprintf("%d results", len(st.results))
	// the row is editable, so say plainly when the hits answer an older question
	if strings.TrimSpace(n.Text()) != st.query {
		head += " for “" + st.query + "” · ⌥r re-runs"
	}
	out := []string{line(th.Dim + head + " · ⌥e opens them" + th.Reset)}
	for i, r := range st.results {
		out = append(out, line(th.Dim+wsRank(i)+" "+th.Reset+th.FG+r.Title+th.Reset+
			th.Dim+" · "+wsHost(r.URL)+th.Reset))
	}
	return out
}

// wsToContext hands the hits to structured outline context: the query, then one
// element per result.
func wsToContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	st := wsStateOf(h, n.UUID())
	var b strings.Builder
	b.WriteString(strings.TrimSpace(n.Text()))
	for _, r := range st.results {
		b.WriteString("\n<result url=\"" + r.URL + "\">" + r.Title)
		if r.Snippet != "" {
			b.WriteString(" — " + r.Snippet)
		}
		b.WriteString("</result>")
	}
	return "websearch", "", b.String()
}

// ── the alt+e view: the same ten hits, with their URLs and snippets ──────────

type wsView struct{}

func (wsView) Enter(h editor.NodeHost, n editor.NodeRef) bool { return true }
func (wsView) Leave(h editor.NodeHost, n editor.NodeRef)      {}

func (v wsView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return len(wsViewLines(h, n, width))
}

func (v wsView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	content := wsViewLines(h, n, width)
	for i, l := range content {
		content[i] = editor.NodeClip(rail+editor.NodeTheme().Reset+l, width)
	}
	return editor.NodeWindowBands(content, scroll, winH)
}

// wsViewLines is the view's full content — one header plus three lines per hit
// (title, url, snippet). Lines and Bands share it so the scroll clamp always
// matches what is drawn.
func wsViewLines(h editor.NodeHost, n editor.NodeRef, width int) []string {
	th := editor.NodeTheme()
	st := wsStateOf(h, n.UUID())

	hdr := "  websearch · "
	switch {
	case st.busy:
		hdr += "searching…"
	case st.errMsg != "":
		hdr += st.errMsg
	default:
		hdr += fmt.Sprintf("%d results", len(st.results))
	}
	out := []string{th.Dim + hdr + " · ↑↓ scroll · ⌥r again · esc close" + th.Reset}
	if len(st.results) == 0 {
		return append(out, th.Dim+"  nothing yet · ⌥r searches the web for this row"+th.Reset)
	}
	for i, r := range st.results {
		out = append(out,
			th.Dim+"  "+wsRank(i)+" "+th.Reset+th.FG+r.Title+th.Reset,
			th.Dim+"     "+r.URL+th.Reset,
		)
		if r.Snippet != "" {
			out = append(out, th.Dim+"     "+r.Snippet+th.Reset)
		}
	}
	return out
}

// Key scrolls the hit list; alt+r searches again. esc falls through to the
// central defocus handler.
func (v wsView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "alt+r":
		return runWebSearch(h, n), true
	case "down", "j":
		h.NodeSetScroll(h.NodeScroll() + 1)
	case "up", "k":
		h.NodeSetScroll(max(0, h.NodeScroll()-1))
	case "pgdown":
		h.NodeSetScroll(h.NodeScroll() + 10)
	case "pgup":
		h.NodeSetScroll(max(0, h.NodeScroll()-10))
	case "home", "g":
		h.NodeSetScroll(0)
	case "end", "G":
		h.NodeSetScroll(1 << 30) // the central clamp pins it to the last page
	default:
		return nil, false
	}
	return nil, true
}
