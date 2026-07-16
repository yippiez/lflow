package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestPathChipTriggerHonorsNodeChipCapability keeps query syntax literal while
// leaving chip gestures available to normal text types. This is descriptor data,
// so another syntax-heavy node can opt out without growing key-handler switches.
func TestPathChipTriggerHonorsNodeChipCapability(t *testing.T) {
	if !pathChipTrigger(database.TypeBullets) {
		t.Error("bullet path chips should stay enabled")
	}
	if pathChipTrigger(database.TypeQuery) {
		t.Error("query > must stay a literal descendant operator")
	}
	if linkChipTrigger(database.TypeQuery) || tagPickerTrigger(database.TypeQuery) {
		t.Error("all automatic chip gestures must be disabled in query syntax")
	}
	if !pathChipTrigger(database.TypeQuote) {
		t.Error("quote path chips should stay enabled")
	}
}

// TestAngleCancelTypesLiteral: dismissing the picker (empty selection) types the
// literal ">" that opened it, so a bash redirect survives a cancelled pick.
func TestAngleCancelTypesLiteral(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "echo hi ", Type: database.TypeQuery})
	cursorOn(m, "edit")
	caret := len([]rune("echo hi "))
	m.caret = caret
	mm, _ := m.Update(fzfPickedMsg{uuid: "edit", caret: caret, path: "", onCancel: ">"})
	*m = *mm.(*Model)

	edit := m.tree.byUUID["edit"]
	if hasAnchor(edit.name) {
		t.Fatalf("a cancelled pick must not create a chip: %q", edit.name)
	}
	if edit.name != "echo hi >" {
		t.Errorf("name = %q, want %q", edit.name, "echo hi >")
	}
	if m.caret != caret+1 {
		t.Errorf("caret = %d, want %d", m.caret, caret+1)
	}
}

// TestAngleKeyTypesLiteralWithoutFzf: when fzf is absent the ">" key falls through
// to typing a literal ">" rather than being swallowed.
func TestAngleKeyTypesLiteralWithoutFzf(t *testing.T) {
	if _, err := exec.LookPath("fzf"); err == nil {
		t.Skip("fzf present: the picker launches instead of typing the literal")
	}
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "echo ", Type: database.TypeQuery})
	cursorOn(m, "edit")
	m.caret = len([]rune("echo "))
	m.press(">")

	edit := m.tree.byUUID["edit"]
	if edit.name != "echo >" {
		t.Errorf("name = %q, want %q", edit.name, "echo >")
	}
}

// TestPathChipInBashNode: a bash node accepts a path chip (inserted by the /file
// picker), rendering it compactly while expanding to the full path for the run —
// so file chips work in bash nodes, not just text nodes.
func TestPathChipInBashNode(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "cat ", Type: database.TypeQuery})
	cursorOn(m, "edit")
	m.caret = len([]rune("cat "))
	// the /file picker resolves its selection through this message.
	mm, _ := m.Update(fzfPickedMsg{uuid: "edit", caret: m.caret, path: "/etc/hosts"})
	*m = *mm.(*Model)

	edit := m.tree.byUUID["edit"]
	if !hasAnchor(edit.name) {
		t.Fatalf("bash node did not get a path chip anchor: %q", edit.name)
	}
	if got := displayAnchors(edit.name, m.chips); got != "cat ›hosts" {
		t.Errorf("display = %q, want %q", got, "cat ›hosts")
	}
	if got := expandAnchors(edit.name, m.chips); got != "cat /etc/hosts" {
		t.Errorf("expand = %q, want %q", got, "cat /etc/hosts")
	}
}

func TestNormalizeFilePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := normalizeFilePath("~/x"); got != filepath.Join(home, "x") {
		t.Fatalf("~ expand: got %q want %q", got, filepath.Join(home, "x"))
	}
	if got := normalizeFilePath("/a/b/../c"); got != "/a/c" {
		t.Fatalf("clean: got %q want /a/c", got)
	}
	if got := normalizeFilePath(""); got != "" {
		t.Fatalf("empty should stay empty, got %q", got)
	}
	// a relative path becomes absolute under the working dir
	wd, _ := os.Getwd()
	if got := normalizeFilePath("rel/path"); got != filepath.Join(wd, "rel/path") {
		t.Fatalf("relative→abs: got %q want %q", got, filepath.Join(wd, "rel/path"))
	}
}
