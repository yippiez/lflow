package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// flashLabels must hand out prefix-free labels: no label may be the start of
// another, or typing a short one would be ambiguous with a longer one.
func TestFlashLabelsArePrefixFree(t *testing.T) {
	for _, n := range []int{1, 5, 26, 27, 50, 200} {
		labels := flashLabels(n)
		if len(labels) != n {
			t.Fatalf("n=%d: got %d labels, want %d", n, len(labels), n)
		}
		seen := map[string]bool{}
		for _, l := range labels {
			if seen[l] {
				t.Fatalf("n=%d: duplicate label %q", n, l)
			}
			seen[l] = true
		}
		for _, a := range labels {
			for _, b := range labels {
				if a != b && strings.HasPrefix(b, a) {
					t.Fatalf("n=%d: label %q is a prefix of %q", n, a, b)
				}
			}
		}
	}
}

// alt+s enters flash mode and labels a jump action on every row except the one
// the cursor is already on (where a jump would be a no-op).
func TestEnterFlashLabelsEveryOtherRow(t *testing.T) {
	m := newTestModel(80, "alpha", "beta", "gamma")
	m.cursor = 0
	m.enterFlash()
	if m.mode != modeFlash {
		t.Fatalf("mode = %d, want modeFlash", m.mode)
	}
	jumps := 0
	for _, tg := range m.flashTargets {
		if tg.verb == "jump" {
			jumps++
			if tg.row == m.cursor {
				t.Fatalf("jump labelled on the cursor row %d", tg.row)
			}
		}
	}
	if jumps != 2 {
		t.Fatalf("jump targets = %d, want 2", jumps)
	}
}

// Typing a complete label fires its action: a jump moves the cursor onto the row
// and leaves flash mode.
func TestFlashJumpFires(t *testing.T) {
	m := newTestModel(80, "alpha", "beta", "gamma")
	m.cursor = 0
	m.enterFlash()
	// find the label that jumps to row 2
	var label string
	for _, tg := range m.flashTargets {
		if tg.verb == "jump" && tg.row == 2 {
			label = tg.label
		}
	}
	if label == "" {
		t.Fatal("no jump target for row 2")
	}
	for _, r := range label {
		m.handleFlashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.mode != modeOutline {
		t.Fatalf("mode = %d, want modeOutline after firing", m.mode)
	}
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.cursor)
	}
}

// A runnable type offers its run action straight from the registry — no
// per-type wiring in flash. Query carries run; the legacy bash type is gone
// (falls back to bullets), so it only offers run when the row has a cmd chip.
func TestFlashOffersRegistryActions(t *testing.T) {
	m := newTestModel(80, "note", "grep foo")
	m.tree.root.children[1].typ = database.TypeQuery
	m.refreshRows()
	m.cursor = 0
	m.enterFlash()
	var run bool
	for _, tg := range m.flashTargets {
		if tg.row == 1 && tg.verb == "run" {
			run = true
		}
	}
	if !run {
		t.Fatal("a query node must offer the run flash action")
	}

	// legacy bash-typed rows are plain bullets now: no type-level run, no expand.
	m.mode = modeOutline
	m.tree.root.children[1].typ = database.TypeBash
	m.refreshRows()
	m.enterFlash()
	for _, tg := range m.flashTargets {
		if tg.row == 1 && (tg.verb == "run" || tg.verb == "expand") {
			t.Fatalf("legacy bash node must offer no %q action", tg.verb)
		}
	}
}

// Content-sensitive alt+r actions are also surfaced in flash: cmd chips get a
// runnable "run $" action, and @mention roots get a send action.
func TestFlashOffersInlineRunActions(t *testing.T) {
	m := newTestModel(80, "note", "")
	m.agents = []tag.Agent{{Name: "Pi", Mock: true}}
	cmdAnchor := m.createChip(chipKindCmd, "echo hi")
	agentAnchor := m.createChip(chipKindAgent, "Pi")
	m.tree.root.children[1].name = "ask " + cmdAnchor + " then " + agentAnchor
	m.refreshRows()
	m.cursor = 0
	m.enterFlash()

	verbs := map[string]bool{}
	for _, tg := range m.flashTargets {
		if tg.row == 1 {
			verbs[tg.verb] = true
		}
	}
	if !verbs["run $"] {
		t.Fatalf("cmd chip should offer a flash run action, got %v", verbs)
	}
	if !verbs["send"] {
		t.Fatalf("@mention should offer a flash send action, got %v", verbs)
	}
}

// A node type can contribute its own named, colored flash actions via the
// registry flashActions hook — voice names them "record" and "play" rather than
// the generic "run"/"expand".
func TestFlashTypeContributedActions(t *testing.T) {
	m := newTestModel(80, "note", "a memo")
	m.tree.root.children[1].typ = database.TypeVoice
	m.refreshRows()
	m.cursor = 0
	m.enterFlash()
	verbs := map[string]bool{}
	for _, tg := range m.flashTargets {
		if tg.row == 1 {
			verbs[tg.verb] = true
		}
	}
	if !verbs["record"] {
		t.Fatalf("voice should contribute a 'record' action, got %v", verbs)
	}
	if verbs["run"] || verbs["expand"] {
		t.Fatalf("voice hook should replace the generic run/expand verbs, got %v", verbs)
	}
}

// Esc cancels flash mode without moving the cursor.
func TestFlashEscCancels(t *testing.T) {
	m := newTestModel(80, "alpha", "beta")
	m.cursor = 1
	m.enterFlash()
	m.handleFlashKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeOutline {
		t.Fatalf("mode = %d, want modeOutline", m.mode)
	}
	if m.cursor != 1 {
		t.Fatalf("cursor moved to %d, want 1", m.cursor)
	}
	if m.flashTargets != nil {
		t.Fatal("flashTargets not cleared")
	}
}

// A chip renders in its action's color; a label that no longer matches the typed
// prefix renders fully gray (no action color).
func TestFlashChipColorsByAction(t *testing.T) {
	jump := flashTarget{label: "a", verb: "jump", color: cAccent}
	run := flashTarget{label: "b", verb: "run", color: cGreen}
	if !strings.Contains(flashChip(jump, ""), cAccent) {
		t.Error("jump chip should use the accent (blue) color")
	}
	if !strings.Contains(flashChip(run, ""), cGreen) {
		t.Error("run chip should use the green color")
	}
	// 'a' typed: the run label 'b' no longer matches → all gray, no action color.
	faded := flashChip(run, "a")
	if strings.Contains(faded, cGreen) {
		t.Error("a non-matching chip must not keep its action color")
	}
	if !strings.Contains(faded, cDim) {
		t.Error("a non-matching chip must render dim/gray")
	}
}

// Typing the first letter of a two-letter label narrows rather than fires:
// flashInput advances and the chip rendering grays the matched prefix.
func TestFlashNarrowsOnPartialLabel(t *testing.T) {
	// 30 rows forces some two-letter labels.
	names := make([]string, 30)
	for i := range names {
		names[i] = "row"
	}
	m := newTestModel(80, names...)
	m.cursor = 0
	m.enterFlash()
	var two string
	for _, tg := range m.flashTargets {
		if len(tg.label) == 2 {
			two = tg.label
			break
		}
	}
	if two == "" {
		t.Fatal("expected at least one two-letter label among 30 rows")
	}
	first := string(two[0])
	m.handleFlashKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(first)})
	if m.mode != modeFlash {
		t.Fatalf("mode = %d, want still modeFlash after one letter", m.mode)
	}
	if m.flashInput != first {
		t.Fatalf("flashInput = %q, want %q", m.flashInput, first)
	}
}
