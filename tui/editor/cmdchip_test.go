package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

func cmdChipOf(m *Model) (database.Chip, bool) {
	for _, c := range m.chips {
		if c.Kind == chipKindBash {
			return c, true
		}
	}
	return database.Chip{}, false
}

// typeCmdChip drives "$$" into the node at the caret: the two-stroke cmd-chip
// trigger. It returns the created chip.
func typeCmdChip(t *testing.T, m *Model) database.Chip {
	t.Helper()
	m.press("$")
	m.press("$")
	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("$$ did not create a bash chip")
	}
	return c
}

// setCmdChipValue writes a command into an empty chip the way alt+i's save
// does — the edited value lands in the chip row and in-memory store.
func setCmdChipValue(m *Model, id, value string) {
	c := m.chips[id]
	c.Value = value
	m.chips[id] = c
	if m.ctx.DB != nil {
		_ = database.UpsertChip(m.ctx.DB, c)
	}
}

// TestCmdChipCreateViaDoubleDollar: typing "$$" lands an EMPTY bash chip — the
// two-stroke trigger. A single "$" stays literal (no chip), and the empty chip
// is the blank to fill in with alt+i.
func TestCmdChipCreateViaDoubleDollar(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0

	m.press("$") // single $: literal, never a chip
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("a single $ must not create a bash chip")
	}
	m.press("$") // second $: lands the empty chip

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("$$ did not create a bash chip")
	}
	if c.Value != "" {
		t.Errorf("$$ should land an EMPTY chip, value=%q", c.Value)
	}
	edit := m.tree.byUUID["edit"]
	if !hasAnchor(edit.name) {
		t.Fatalf("node has no chip anchor: %q", edit.name)
	}
	if got := displayAnchors(edit.name, m.chips); got != "$ " {
		t.Errorf("rendered = %q, want %q", got, "$ ")
	}
}

// TestCmdChipMidSentence: "$$" dropped mid-text lands the empty chip, leaving
// the surrounding prose intact.
func TestCmdChipMidSentence(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "run "})
	cursorOn(m, "edit")
	m.caret = len([]rune("run "))

	m.press("$")
	m.press("$")

	if _, ok := cmdChipOf(m); !ok {
		t.Fatal("no bash chip created")
	}
	if got := displayAnchors(m.tree.byUUID["edit"].name, m.chips); got != "run $ " {
		t.Errorf("rendered = %q, want %q", got, "run $ ")
	}
}

// TestCmdChipSingleDollarStaysLiteral: a lone "$" in prose never chips — "$i",
// "$(seq …)", "$HOME" are ordinary text until "$$" is typed.
func TestCmdChipSingleDollarStaysLiteral(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "run "})
	cursorOn(m, "edit")
	m.caret = len([]rune("run "))

	m.press("$i") // a variable-looking token
	m.press(" ")
	m.press(" ")
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("$i + double space must stay literal text, not chip")
	}
	if got := m.tree.byUUID["edit"].name; got != "run $i  " {
		t.Errorf("node text = %q, want the literal $i", got)
	}
}

// TestCmdChipPreviewIsEphemeral: a run preview lands in the in-memory chip label
// (shown as "$cmd → preview") but is never written to the chips table — the
// command persists; → chrome is rehydrated from local node_output, not stored
// on the chip row.
func TestCmdChipPreviewIsEphemeral(t *testing.T) {
	m, db := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	c := typeCmdChip(t, m)
	setCmdChipValue(m, c.ID, "ls") // what alt+i would save
	m.ensureRun(c.ID).out = []outLine{{text: "file-a"}, {text: "file-b"}}
	m.setBashChipPreview(c.ID)

	if got := chipDisplay(m.chips[c.ID]); got != "$ ls → file-a" {
		t.Errorf("chip display = %q, want %q", got, "$ ls → file-a")
	}
	rendered := renderBashChip(m.chips[c.ID], false)
	preview := cDim + " → file-a"
	if !strings.Contains(rendered, bgCode+cRed+"$ "+cFG+"ls"+cReset+preview) {
		t.Errorf("preview should be muted after a background reset, got %q", rendered)
	}
	// the persisted chip row must keep an empty label (preview is not chip data)
	stored, err := database.GetChip(db, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Label != "" {
		t.Errorf("persisted label = %q, want empty (preview must not write the chip row)", stored.Label)
	}
}

// TestCmdChipPreviewRehydratesOnOpen: after a run is persisted to node_output,
// a fresh Model with only LoadChips + hydrateBashChipPreviews rebuilds → chrome
// without re-running and without writing the chip row.
func TestCmdChipPreviewRehydratesOnOpen(t *testing.T) {
	m, db := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	c := typeCmdChip(t, m)
	setCmdChipValue(m, c.ID, "ls")
	m.ensureRun(c.ID).out = []outLine{{text: "  "}, {text: "file-a"}, {text: "file-b"}}
	m.persistRunOut(c.ID)
	m.setBashChipPreview(c.ID)
	if got := m.chips[c.ID].Label; got != "file-a" {
		t.Fatalf("seed label = %q, want file-a", got)
	}

	// simulate a new client: chips from DB (empty labels), no runs map
	chips, err := database.LoadChips(db)
	if err != nil {
		t.Fatal(err)
	}
	if chips[c.ID].Label != "" {
		t.Fatalf("LoadChips label = %q, want empty", chips[c.ID].Label)
	}
	reopened := &Model{ctx: m.ctx, chips: chips}
	reopened.hydrateBashChipPreviews()

	if got := reopened.chips[c.ID].Label; got != "file-a" {
		t.Errorf("rehydrated label = %q, want file-a", got)
	}
	if got := chipDisplay(reopened.chips[c.ID]); got != "$ ls → file-a" {
		t.Errorf("chip display = %q, want %q", got, "$ ls → file-a")
	}
	stored, err := database.GetChip(db, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Label != "" {
		t.Errorf("chip row label = %q after hydrate, want empty", stored.Label)
	}
}

// TestCmdChipNotInBashNode: a Bash node's text IS a shell command, so "$" and
// "#" are its own syntax — typing "$i", "$(seq …)" and double spaces must NOT
// chip inside it. A "$$" still lands an empty chip by deliberate trigger.
func TestCmdChipNotInBashNode(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "", Type: database.TypeBash})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("for i in $(seq 1 5); do echo $i; done")
	m.press(" ")
	m.press(" ")
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("a bash node auto-chipped its $ syntax — $ is the command, not a chip")
	}
	if got := m.tree.byUUID["edit"].name; got != "for i in $(seq 1 5); do echo $i; done  " {
		t.Errorf("bash node text changed to %q — the $ must stay literal", got)
	}
	// a deliberate "$$" still lands a chip even on a bash row — the trigger is
	// explicit, not an accident of $ appearing in the command
	m2, _ := dbModel(t, database.Node{UUID: "edit", Name: "echo "})
	cursorOn(m2, "edit")
	m2.caret = len([]rune("echo "))
	m2.press("$")
	m2.press("$")
	if _, ok := cmdChipOf(m2); !ok {
		t.Fatal("a deliberate $$ should land a chip even on a bash node")
	}
}

// TestCmdChipAltEFocusesInlineBand: alt+e on a bash chip focuses its inline
// output band (the bash-node surface) instead of a separate page — the editor
// stays in modeOutline — and alt+e (or esc) defocuses it again.
func TestCmdChipAltEFocusesInlineBand(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	c := typeCmdChip(t, m)
	// park the caret right after the chip anchor so bashChipAtCaret finds it
	spans := anchorSpans([]rune(m.tree.byUUID["edit"].name))
	if len(spans) != 1 {
		t.Fatalf("want 1 anchor span, got %d", len(spans))
	}
	m.caret = spans[0].end

	m.press("alt+e")
	if !m.focused || m.focusChip != c.ID {
		t.Fatalf("alt+e should focus the chip band: focused=%v focusChip=%q", m.focused, m.focusChip)
	}
	if m.mode != modeOutline {
		t.Fatalf("chip band must stay inline: mode=%v", m.mode)
	}

	m.press("alt+e")
	if m.focused || m.focusChip != "" {
		t.Fatalf("alt+e again should defocus: focused=%v focusChip=%q", m.focused, m.focusChip)
	}
}

// TestCmdChipAltIEditsCommand: alt+i on a bash chip opens the one-field command
// editor (chipEditView) — the chip's only edit, since alt+e is its run band and
// delete would drop the whole chip — and enter saves the new command back to the
// chip, dropping the stale run band so the old result does not read as this
// command's output.
func TestCmdChipAltIEditsCommand(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	c := typeCmdChip(t, m)
	// a finished run sits on the chip (stale once the command changes)
	m.ensureRun(c.ID).out = []outLine{{text: "file-a"}}
	m.setBashChipPreview(c.ID)

	spans := anchorSpans([]rune(m.tree.byUUID["edit"].name))
	m.caret = spans[0].end

	m.press("alt+i")
	if !m.focused || m.focusChip != c.ID || !m.focusChipEditing {
		t.Fatalf("alt+i should open the command editor: focused=%v focusChip=%q editing=%v", m.focused, m.focusChip, m.focusChipEditing)
	}
	// the working copy starts empty ($$ lands an empty chip); type the command
	for _, r := range "date +%s" {
		m.press("" + string(r))
	}
	if m.chipEditValue != "date +%s" {
		t.Fatalf("chipEditValue = %q, want the edited command", m.chipEditValue)
	}
	m.press("enter")
	if m.focused || m.focusChip != "" {
		t.Fatalf("enter should close the editor: focused=%v focusChip=%q", m.focused, m.focusChip)
	}
	got, _ := cmdChipOf(m)
	if got.Value != "date +%s" {
		t.Errorf("chip value = %q, want the saved command", got.Value)
	}
	// the stale run band is gone — the preview no longer reads "file-a"
	m.setBashChipPreview(got.ID)
	if got.Label != "" {
		t.Errorf("stale preview survived a command edit: %q", got.Label)
	}
}
