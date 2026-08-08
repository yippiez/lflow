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
