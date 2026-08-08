package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// TestQuickReplyBoxAndSend: ⌥m raises the rounded box, and enter ships the
// field to the live session's PTY — text as a paste, Enter as its own delayed
// write — which the child actually receives.
func TestQuickReplyBoxAndSend(t *testing.T) {
	m := &Model{width: 80, height: 24, chips: map[string]database.Chip{
		"chip-r": {ID: "chip-r", Kind: chipKindAgent, Value: "claude", Label: "helper"},
	}}
	// the box header reads the SESSION's name (agentTitle), not the chip label
	m.nodeStore("chip-r")["agentSession"] = agentSession{Variant: "claude", Name: "helper"}
	s := fakeAgentSession(t, m, "chip-r", `read -r line; printf 'got:%s' "$line"; sleep 2`)

	m.openQuickReply(m.chips["chip-r"])
	if !m.quickReply || m.focusChip != "chip-r" || !m.focused {
		t.Fatal("openQuickReply did not raise the box")
	}
	f := m.quickReplyField("chip-r")
	if f == nil {
		t.Fatal("no reply field")
	}

	rows := quickReplyView{}.bands(m, nil, "", 80, 0, 10, true)
	if len(rows) != 4 {
		t.Fatalf("box = %d rows, want 4", len(rows))
	}
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"╭", "╰", "❯", "helper"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("box missing %q:\n%s", want, joined)
		}
	}

	f.value = "ping"
	f.caret = 4
	m.sendQuickReply("chip-r", f)
	if f.value != "" {
		t.Fatal("a sent reply must clear the field")
	}
	drainUntil(t, m, s, func() bool {
		return strings.Contains(strings.Join(s.Scr.GridPlain(), "\n"), "got:ping")
	})

	// closing the box drops its state
	quickReplyView{}.leave(m, nil)
	if m.quickReply || m.focusChip != "" || m.quickReplyField("chip-r") != nil {
		t.Fatal("leave did not clear the box")
	}
}

// TestQuickReplyNeedsLiveSession: enter without a live session flashes, never
// panics, and keeps the text.
func TestQuickReplyNeedsLiveSession(t *testing.T) {
	m := &Model{width: 80, height: 24, chips: map[string]database.Chip{
		"chip-d": {ID: "chip-d", Kind: chipKindAgent, Value: "claude"},
	}}
	m.openQuickReply(m.chips["chip-d"])
	f := m.quickReplyField("chip-d")
	f.value = "hello"
	m.sendQuickReply("chip-d", f)
	if !m.flashErr || f.value != "hello" {
		t.Fatalf("dead-session send must warn and keep the text, flash = %q", m.flash)
	}
}

// TestQuickReplyBoxGeometry: the box survives hostile content — a session
// title longer than the box, a last response full of wide runes, a reply
// longer than the line — with every border and every rounded corner still on
// the pane at its exact column. This is the "corner sheared off" regression:
// the old header only filled when there was room, so a long real-session
// title pushed ╮ past the clip and the box opened up.
func TestQuickReplyBoxGeometry(t *testing.T) {
	m := &Model{width: 80, height: 24, chips: map[string]database.Chip{
		"chip-g": {ID: "chip-g", Kind: chipKindAgent, Value: "claude"},
	}}
	m.nodeStore("chip-g")["agentSession"] = agentSession{
		Variant: "claude",
		Name:    "a very long session title that keeps going well past any box width limit whatsoever",
	}
	m.openQuickReply(m.chips["chip-g"])
	f := m.quickReplyField("chip-g")
	f.value = strings.Repeat("類", 60) // wide runes, twice the box
	f.caret = 60

	rows := quickReplyView{}.bands(m, nil, "  ", 80, 0, 10, true)
	if len(rows) != 4 {
		t.Fatalf("box = %d rows, want 4", len(rows))
	}
	widths := make([]int, len(rows))
	for i, r := range rows {
		widths[i] = visibleWidth(r)
		if widths[i] > 80 {
			t.Fatalf("row %d is %d cols wide, spills the 80-col pane:\n%s", i, widths[i], r)
		}
	}
	for i := 1; i < len(rows); i++ {
		if widths[i] != widths[0] {
			t.Fatalf("ragged right edge: row %d is %d cols, row 0 is %d\n%s",
				i, widths[i], widths[0], strings.Join(rows, "\n"))
		}
	}
	for i, corner := range []string{"╮", "│", "│", "╯"} {
		if !strings.HasSuffix(stripSGR(rows[i]), corner) {
			t.Fatalf("row %d does not end with %q:\n%s", i, corner, stripSGR(rows[i]))
		}
	}
	// the caret end of the long reply is in view, not clipped off past the border
	if !strings.Contains(rows[2], "…") {
		t.Fatal("a reply wider than the box must slide left under the caret")
	}
}

// TestCaretWindow: the reply line slides so the caret never leaves the box.
func TestCaretWindow(t *testing.T) {
	if got := caretWindow("short", 5, 40); !strings.Contains(got, "short") {
		t.Fatalf("short value must render whole, got %q", got)
	}
	long := strings.Repeat("x", 50) + "END"
	got := caretWindow(long, 53, 20)
	if !strings.Contains(got, "END") {
		t.Fatalf("caret tail must stay visible, got %q", got)
	}
	if !strings.HasPrefix(got, cDim+"…") {
		t.Fatalf("slid window must mark the cut, got %q", got)
	}
}
