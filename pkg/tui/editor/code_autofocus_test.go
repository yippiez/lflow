package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// codeAutoModel seeds root → top(bullet), code(code node), bot(bullet) with the
// cursor parked on the code node.
func codeAutoModel() (*Model, *item) {
	root := &item{}
	top := &item{uuid: "top", name: "top", parent: root, typ: database.TypeBullets}
	code := &item{uuid: "code", name: "", parent: root, typ: database.TypeCode}
	bot := &item{uuid: "bot", name: "bot", parent: root, typ: database.TypeBullets}
	root.children = []*item{top, code, bot}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"top": top, "code": code, "bot": bot},
		externalNames: map[string]string{},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.cursor = m.rowIndexOf(code)
	return m, code
}

// step mirrors Update's KeyMsg path: handle the key, then reconcile auto-focus.
func (m *Model) step(s string) {
	m.handleKey(key(s))
	m.reconcileAutoFocus()
}

// TestCodeAutoFocusTyping: resting the cursor on a code node auto-focuses its
// block, so a keystroke lands in the block buffer with no alt+e.
func TestCodeAutoFocusTyping(t *testing.T) {
	m, code := codeAutoModel()

	m.step("x")
	if !m.focused || m.autoFocused != code {
		t.Fatalf("cursor on a code node must auto-focus it, focused=%v autoFocused=%v", m.focused, m.autoFocused)
	}
	if buf, _ := (codeView{}).get(m, code); buf != "x" {
		t.Fatalf("typed rune must land in the block buffer, got %q", buf)
	}
	m.step("y")
	if buf, _ := (codeView{}).get(m, code); buf != "xy" {
		t.Fatalf("block buffer = %q, want xy", buf)
	}
}

// TestCodeAutoFocusDownReleases: Down on the block's last line flushes the
// buffer and crosses to the next row, dropping focus.
func TestCodeAutoFocusDownReleases(t *testing.T) {
	m, code := codeAutoModel()
	m.step("x") // enter + type
	m.step("down")

	if m.focused || m.autoFocused != nil {
		t.Fatalf("Down off the last line must release focus, focused=%v", m.focused)
	}
	if code.name != "x" {
		t.Fatalf("the block buffer must flush to the node on leave, name=%q", code.name)
	}
	if got := m.cursorItem(); got == nil || got.uuid != "bot" {
		t.Fatalf("cursor must cross to the next row, got %v", got)
	}
}

// TestCodeAutoFocusUpReleases: Up on the block's first line crosses to the
// previous row.
func TestCodeAutoFocusUpReleases(t *testing.T) {
	m, _ := codeAutoModel()
	m.step("x")
	m.step("up")

	if m.focused {
		t.Fatal("Up off the first line must release focus")
	}
	if got := m.cursorItem(); got == nil || got.uuid != "top" {
		t.Fatalf("cursor must cross to the previous row, got %v", got)
	}
}

// TestCodeAutoFocusEnterNewline: Enter inside the block is a newline, not a
// row split — so a code node stays one node with a two-line body.
func TestCodeAutoFocusEnterNewline(t *testing.T) {
	m, code := codeAutoModel()
	m.step("a")
	m.step("enter")
	m.step("b")

	if buf, _ := (codeView{}).get(m, code); buf != "a\nb" {
		t.Fatalf("Enter must be a newline inside the block, buffer=%q", buf)
	}
	if len(m.tree.root.children) != 3 {
		t.Fatalf("Enter must not split the code node, %d siblings", len(m.tree.root.children))
	}
}

// TestCodeAutoFocusEscHolds: esc parks the block unfocused and does not
// immediately re-grab it — so structural keys can then act on the node.
func TestCodeAutoFocusEscHolds(t *testing.T) {
	m, code := codeAutoModel()
	m.step("x")
	m.step("esc")

	if m.focused || m.autoFocused != nil {
		t.Fatalf("esc must drop focus, focused=%v", m.focused)
	}
	if m.autoFocusHold != code {
		t.Fatalf("esc must hold the node against re-grab, hold=%v", m.autoFocusHold)
	}
	// a second reconcile (as the next key would trigger) must NOT re-focus it
	m.reconcileAutoFocus()
	if m.focused {
		t.Fatal("the held node must not be auto-re-focused while the cursor stays")
	}
}

// TestCodeBlockKeepsGlyph: the code block's first line still shows the ○ bullet
// glyph — the block replaces the row but the node stays visible as a node.
func TestCodeBlockKeepsGlyph(t *testing.T) {
	m, _ := codeAutoModel()
	m.cursor = m.rowIndexOf(m.tree.byUUID["top"]) // don't auto-focus the code row
	out := m.View()
	if !containsGlyphOnCodeRow(out) {
		t.Fatalf("code block first line must keep the ○ glyph:\n%s", out)
	}
}

func containsGlyphOnCodeRow(view string) bool {
	// the code block draws the dim line-number gutter "1 │"; the same first line
	// must carry the ○ bullet before it.
	for _, ln := range strings.Split(view, "\n") {
		if strings.Contains(ln, glyphOpen) && strings.Contains(ln, "│") {
			return true
		}
	}
	return false
}
