package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func cmdChipOf(m *Model) (database.Chip, bool) {
	for _, c := range m.chips {
		if c.Kind == chipKindCmd {
			return c, true
		}
	}
	return database.Chip{}, false
}

// TestCmdChipCreateViaDoubleSpace: typing "$ls -la" then a DOUBLE space commits an
// inline cmd chip whose value is the command (single spaces stay in the command).
func TestCmdChipCreateViaDoubleSpace(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0

	m.press("$ls -la")
	m.press(" ") // first space: stays part of the command
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("a single space must not commit the cmd chip")
	}
	m.press(" ") // second space: commits the chip

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("double space did not create a cmd chip")
	}
	if c.Value != "ls -la" {
		t.Errorf("cmd value = %q, want %q", c.Value, "ls -la")
	}
	edit := m.tree.byUUID["edit"]
	if !hasAnchor(edit.name) {
		t.Fatalf("node has no chip anchor: %q", edit.name)
	}
	if got := displayAnchors(edit.name, m.chips); got != "$ ls -la " {
		t.Errorf("rendered = %q, want %q", got, "$ ls -la ")
	}
}

// TestCmdChipMidSentence: a "$cmd" token dropped mid-text (preceded by a space)
// converts, leaving the surrounding prose intact.
func TestCmdChipMidSentence(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "run "})
	cursorOn(m, "edit")
	m.caret = len([]rune("run "))

	m.press("$echo hi")
	m.press(" ")
	m.press(" ")

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("no cmd chip created")
	}
	if c.Value != "echo hi" {
		t.Errorf("cmd value = %q, want %q", c.Value, "echo hi")
	}
	if got := displayAnchors(m.tree.byUUID["edit"].name, m.chips); got != "run $ echo hi " {
		t.Errorf("rendered = %q, want %q", got, "run $ echo hi ")
	}
}

// TestCmdChipDraftColorsImmediately: a standalone "$" starts a live cmd-chip
// draft before it is committed, so the prompt is red and the command area is
// already on the same gray background the final chip will use.
func TestCmdChipDraftColorsImmediately(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0

	m.press("$ls")
	edit := m.tree.byUUID["edit"]
	if !m.cmdDraftLive(edit) {
		t.Fatal("typing a standalone $ should leave the cmd draft live")
	}
	rendered := renderBody(edit, edit.name, m.caret, true, m.chips, m.cmdDraftLive(edit))
	if !strings.Contains(rendered, bgCode) || !strings.Contains(rendered, cRed+"$") {
		t.Fatalf("live cmd draft should have gray bg and red prompt, got %q", rendered)
	}
	if _, ok := cmdChipOf(m); ok {
		t.Fatal("draft must not become a chip until double space")
	}
}

// TestCmdDraftEndsOnCaretMove: the draft tint is a typing affordance, not a
// property of the text — walking the caret into pre-existing "$…" prose (e.g.
// ordinary prose quoting a command) must NOT tint it as a code cell.
func TestCmdDraftEndsOnCaretMove(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "run $ ls to list files"})
	cursorOn(m, "edit")
	m.caret = len([]rune("run $ ls to l")) // caret parked mid-text, no typing

	edit := m.tree.byUUID["edit"]
	if m.cmdDraftLive(edit) {
		t.Fatal("a caret move alone must not start a cmd draft")
	}
	rendered := renderBody(edit, edit.name, m.caret, true, m.chips, m.cmdDraftLive(edit))
	if strings.Contains(rendered, bgCode) {
		t.Fatalf("pre-existing $ text must render plain on a caret walk, got %q", rendered)
	}

	// typing revives the draft at the caret; moving the caret ends it again
	m.press("x")
	if !m.cmdDraftLive(edit) {
		t.Fatal("typing after a $ token should make the draft live")
	}
	m.press("left")
	if m.cmdDraftLive(edit) {
		t.Fatal("moving the caret should end the live draft")
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
	m.press("$ls")
	m.press(" ")
	m.press(" ")

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("no cmd chip created")
	}
	m.ensureRun(c.ID).out = []outLine{{text: "file-a"}, {text: "file-b"}}
	m.setCmdPreview(c.ID)

	if got := chipDisplay(m.chips[c.ID]); got != "$ ls → file-a" {
		t.Errorf("chip display = %q, want %q", got, "$ ls → file-a")
	}
	rendered := renderCmdChip(m.chips[c.ID], false)
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
// a fresh Model with only LoadChips + hydrateCmdPreviews rebuilds → chrome
// without re-running and without writing the chip row.
func TestCmdChipPreviewRehydratesOnOpen(t *testing.T) {
	m, db := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("$ls")
	m.press(" ")
	m.press(" ")

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("no cmd chip created")
	}
	m.ensureRun(c.ID).out = []outLine{{text: "  "}, {text: "file-a"}, {text: "file-b"}}
	m.persistRunOut(c.ID)
	m.setCmdPreview(c.ID)
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
	reopened.hydrateCmdPreviews()

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

// TestCmdChipFoldsInPathChip: a path chip spliced into a "$…" command (e.g. via
// the ">" picker) is folded into the cmd chip's value as its full path when the
// double space commits, and the now-orphaned path chip record is dropped.
func TestCmdChipFoldsInPathChip(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("$cat ")
	// splice a path chip at the caret, as the ">" fuzzy picker would.
	m.insertPathChip(m.tree.byUUID["edit"], m.caret, "/etc/hosts")
	pathID := ""
	for id, c := range m.chips {
		if c.Kind == chipKindPath {
			pathID = id
		}
	}
	if pathID == "" {
		t.Fatal("path chip was not inserted")
	}
	m.press(" ") // first space
	m.press(" ") // second space commits the cmd chip

	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("a path chip inside the command blocked cmd chip creation")
	}
	if c.Value != "cat /etc/hosts" {
		t.Errorf("cmd value = %q, want %q", c.Value, "cat /etc/hosts")
	}
	if _, ok := m.chips[pathID]; ok {
		t.Errorf("folded path chip %q should have been deleted", pathID)
	}
	// only the cmd chip's anchor remains in the name.
	if got := displayAnchors(m.tree.byUUID["edit"].name, m.chips); got != "$ cat /etc/hosts " {
		t.Errorf("rendered = %q, want %q", got, "$ cat /etc/hosts ")
	}
}

// TestCmdChipInLegacyBashNode: the bash node type is gone — a legacy
// "bash"-typed row falls back to bullets, so cmd chips form there like in any
// text node.
func TestCmdChipInLegacyBashNode(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "", Type: database.TypeBash})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("$ls -la")
	m.press(" ")
	m.press(" ")
	if c, ok := cmdChipOf(m); !ok || c.Value != "ls -la" {
		t.Fatalf("legacy bash-typed nodes chip like bullets, got ok=%v", ok)
	}
}

// TestCmdChipAltEFocusesInlineBand: alt+e on a cmd chip focuses its inline
// output band (the bash-node surface) instead of a separate page — the editor
// stays in modeOutline — and alt+e (or esc) defocuses it again.
func TestCmdChipAltEFocusesInlineBand(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0
	m.press("$echo hi")
	m.press(" ")
	m.press(" ")
	c, ok := cmdChipOf(m)
	if !ok {
		t.Fatal("no cmd chip created")
	}
	// park the caret right after the chip anchor so cmdChipAtCaret finds it
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
