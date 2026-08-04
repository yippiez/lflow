package editor

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Transcribing a session into the outline.
//
// An Agent node's children ARE its transcript: one node per turn — what you
// asked, what the agent said, each tool it called, and a Diff node for every
// write tool that recorded what it wrote. They are REAL nodes, persisted like
// any other, so they can be folded, zoomed, linked to, searched and queried.
// They are also LOCKED (readonly + structureLocked): the transcript is a record
// of something that already happened, so it is yours to read and to point at,
// never to edit. Editing it would make the outline disagree with the session it
// claims to quote.
//
// The Agent row itself is locked for the same reason — its text is the session's
// name, and it is renamed with ⌥n rather than by typing over it.
//
// ⌥e transcribes (and re-transcribes: a session that has moved on since gains
// its new turns and keeps the uuids of the ones already there, so a link into
// the middle of a transcript survives a refresh).

// agentTranscribe replaces an Agent node's children with its session's turns.
// Nodes already standing for a turn are REUSED in order, so re-running after the
// session grows appends rather than rebuilding — links, folds and stars pointed
// at an existing turn survive.
func (m *Model) agentTranscribe(it *item) tea.Cmd {
	h, ok := agentNodeHandle(m, it)
	if !ok {
		return m.openAgentPickerForNode(it.uuid)
	}
	m.pushUndo("")
	m.refreshAgentTrace(h)
	traces := m.agentTraces(h)
	if len(traces) == 0 {
		m.flash = h.v.label + " · nothing recorded yet"
		return nil
	}

	// existing turn nodes, in order — anything else the user parked under the
	// agent node is left alone and follows the transcript
	var reuse, others []*item
	for _, c := range it.children {
		if c.structureLocked && c.readonly {
			reuse = append(reuse, c)
			continue
		}
		others = append(others, c)
	}

	kept := make([]*item, 0, len(traces))
	for i, tr := range traces {
		var c *item
		if i < len(reuse) {
			c = reuse[i]
		} else {
			var err error
			if c, err = m.tree.newItem(); err != nil {
				break
			}
		}
		c.parent = it
		c.collapsed = false
		applyTraceNode(c, tr)
		kept = append(kept, c)
	}
	for _, stale := range reuse[min(len(reuse), len(kept)):] {
		m.tree.remove(stale)
	}
	it.children = append(kept, others...)
	it.collapsed = false
	m.unsaved = true
	m.refreshRows()
	m.flash = h.v.label + " · " + plural(len(kept), "turn") + " transcribed"
	return nil
}

// applyTraceNode shapes one node to one turn: a Diff node when the turn carried
// a file change, an ordinary locked row otherwise, prefixed with who spoke so
// the transcript reads without a legend.
func applyTraceNode(c *item, tr agentTrace) {
	c.readonly = true
	c.structureLocked = true
	c.style = ""
	if tr.diff != nil {
		c.typ = database.TypeDiff
		c.name = diffBody(tr.diff)
		return
	}
	c.typ = database.TypeBullets
	c.name = traceSpeaker(tr.kind) + tr.text
}

// traceSpeaker is the short mark a transcript row wears, so a wall of turns
// still says who is talking at a glance. It is part of the node's TEXT rather
// than a glyph, because these nodes travel — into a query hit, an export, a
// link — and the mark has to travel with them.
func traceSpeaker(kind string) string {
	switch kind {
	case "user":
		return "you · "
	case "assistant":
		return "agent · "
	case "tool":
		return "$ "
	case "result":
		return "→ "
	case "thinking":
		return "thinking · "
	}
	return ""
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// agentNodeRender draws the Agent row as the session's PILL — the same filled
// chip the inline handle wears. The node and the chip are two placements of one
// session, so they read the same; a row that showed the bare title instead was
// the only surface where a session did not look like itself.
func (m *Model) agentNodeRender(it *item) string {
	s := m.agentLoad(it.uuid)
	v, ok := agentVariantByID(s.Variant)
	if !ok {
		return cDim + "◈ agent · ⌥r picks a session" + cReset
	}
	label := m.agentTitle(it.uuid, v, s)
	pill := renderAgentChip(database.Chip{ID: it.uuid, Kind: chipKindAgent, Value: v.id, Label: label}, false, false)
	tail := ""
	if n := lockedTurnCount(it); n > 0 {
		tail = cDim + " · " + plural(n, "turn") + cReset
	} else {
		tail = cDim + " · ⌥e transcribe" + cReset
	}
	return pill + tail
}

// lockedTurnCount counts the transcript nodes under an Agent node — the locked
// ones it wrote, never whatever else was parked there.
func lockedTurnCount(it *item) int {
	n := 0
	for _, c := range it.children {
		if c.readonly && c.structureLocked {
			n++
		}
	}
	return n
}

// agentNodeFlashActions: ⌥r hands over the terminal, ⌥e writes the transcript
// into the outline, and the two identity edits stay where they are.
func agentNodeFlashActions(m *Model, it *item) []flashAction {
	return []flashAction{
		{verb: "open", color: cGreen, do: runAgentNode},
		{verb: "transcribe", color: cCyan, do: func(m *Model, it *item) tea.Cmd { return m.agentTranscribe(it) }},
	}
}

// agentNodeToContextBody gives an Agent node the session's name and lets the
// transcript nest as its children, which is exactly what they are.
func agentTurnToContext(it *item) contextXML {
	kind, text := "turn", it.name
	for _, prefix := range []struct{ mark, name string }{
		{"you · ", "user"}, {"agent · ", "assistant"}, {"$ ", "tool"},
		{"→ ", "result"}, {"thinking · ", "thinking"},
	} {
		if rest, ok := strings.CutPrefix(it.name, prefix.mark); ok {
			kind, text = prefix.name, rest
			break
		}
	}
	return contextXML{tag: "turn", attrs: attrIfSet("by", kind), body: text}
}
