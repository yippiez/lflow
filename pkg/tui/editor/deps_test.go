package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// TestMissingDepGates: a missing CLI dependency greys the /type entry,
// refuses the pick and the @mention completion, and errors a run — all with
// "Missing dependency: <bin>".
func TestMissingDepGates(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	m.deps = map[string]bool{"pi": false, "ffmpeg": false}

	// running a mention whose backend is missing errors in the bar
	m.agents = []tag.Agent{{Name: "Pi"}} // the real CLI-backed Pi, not the mock
	m.tagClients = nil
	if cmd := m.sendThread(n1, m.agents[0]); cmd != nil {
		t.Fatal("a dep-missing agent must not send")
	}
	if m.agentErr != "Missing dependency: pi" {
		t.Fatalf("agentErr = %q", m.agentErr)
	}

	// the voice type is dep-gated on ffmpeg
	if bin, missing := m.typeDepMissing(database.TypeVoice); !missing || bin != "ffmpeg" {
		t.Fatalf("voice dep = %q missing=%v", bin, missing)
	}
	// /type pick refuses and flashes
	mm, _ := typeSource{}.onSelect(m, pickerItem{value: database.TypeVoice})
	if mm.(*Model).flash != "Missing dependency: ffmpeg" {
		t.Fatalf("type pick flash = %q", m.flash)
	}
	// the picker row renders greyed with the missing bin named
	var row pickerItem
	for _, it := range (typeSource{}).items(m, "voice") {
		if it.value == database.TypeVoice {
			row = it
		}
	}
	if row.render == nil || !strings.Contains(row.render(false), "missing ffmpeg") {
		t.Fatal("voice row must render disabled")
	}

	// the @ completer refuses a dep-missing agent — no chip lands
	before := n1.name
	m.compl = complState{kind: complAgent, start: 0}
	m.applyCompletion(n1, pickerItem{label: "@Pi", value: "Pi"})
	if n1.name != before {
		t.Fatal("a refused mention must not change the node")
	}
	if m.flash != "Missing dependency: pi" {
		t.Fatalf("completer flash = %q", m.flash)
	}

	// availability restores everything
	m.deps = map[string]bool{"pi": true, "ffmpeg": true}
	if _, missing := m.typeDepMissing(database.TypeVoice); missing {
		t.Fatal("available dep must not gate")
	}
	// mock/websocket agents are never CLI-gated
	if _, missing := m.agentDepMissing(tag.Agent{Name: "Pi", Mock: true}); missing {
		t.Fatal("a mock agent has no CLI dep")
	}
}
