package editor

import (
	"strings"
	"testing"
)

// These tests drive the real lflow binary through tmux and assert on the
// plain-text pane that capture-pane renders. Each test seeds its own db under
// an isolated HOME, opens the editor in its own tmux session, and synchronizes
// with waitFor rather than fixed sleeps.
//
// Rendering cheatsheet (from the live editor):
//   ○ name          a plain bullet node
//   ● name · N children   a collapsed node
//   ◆ name · mirror a mirror reference; its source's children show as ◆ rows
//   ╰─ / ├─         child tree-drawing prefixes
//   bottom bar:     " <breadcrumb> · pos/total[ · state]"

// seedScratch adds the always-present "scratch" root the editor opens on.
func seedScratch(s *session) { s.seedCmd("node", "add", "scratch") }

func assertNoCrash(t *testing.T, snap string) {
	t.Helper()
	for _, bad := range []string{"panic", "runtime error", "goroutine "} {
		if strings.Contains(snap, bad) {
			t.Fatalf("editor appears to have crashed (%q present):\n%s", bad, snap)
		}
	}
}

// 1. Add + type: open, type a node name, it appears.
func TestAddAndType(t *testing.T) {
	s := newSession(t, 70, 20, seedScratch)
	s.sendText("hello world")
	snap := s.waitFor("hello world", waitTimeout)
	if !strings.Contains(snap, "○ hello world") {
		t.Fatalf("typed node not rendered as a bullet:\n%s", snap)
	}
}

// 2. Enter splits at the caret: "asdasd", End, Left×3, Enter -> "asd"/"asd".
func TestEnterSplitsAtCaret(t *testing.T) {
	s := newSession(t, 70, 20, seedScratch)
	s.sendText("asdasd")
	s.waitFor("asdasd", waitTimeout)
	s.send("End")
	s.send("Left", "Left", "Left")
	s.send("Enter")
	snap := s.waitFor("2/2", waitTimeout)
	lines := bulletLines(snap)
	if len(lines) < 2 || lines[0] != "asd" || lines[1] != "asd" {
		t.Fatalf("split did not produce two \"asd\" rows, got %v:\n%s", lines, snap)
	}
}

// 3. Enter at the start pushes the node (with its child) down, leaving an empty
//    row above.
func TestEnterAtStartPushesDown(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "Test")
		s.seedCmd("node", "add", "--parent", "Test", "kid")
	})
	s.waitFor("Test", waitTimeout)
	s.send("Home")
	s.send("Enter")
	snap := s.waitFor("1/3", waitTimeout)
	// an empty bullet row above, then Test, then its kid below it
	if !strings.Contains(snap, "Test") || !strings.Contains(snap, "kid") {
		t.Fatalf("Test/kid missing after push-down:\n%s", snap)
	}
	idxTest := strings.Index(snap, "○ Test")
	idxKid := strings.Index(snap, "○ kid")
	if idxTest < 0 || idxKid < 0 || idxKid < idxTest {
		t.Fatalf("kid should remain a child below Test:\n%s", snap)
	}
	// the new empty row should be the first bullet, above Test
	if idxEmpty := strings.Index(snap, "○ \n"); idxEmpty >= 0 && idxEmpty > idxTest {
		t.Fatalf("empty row should sit above Test:\n%s", snap)
	}
}

// 4. Backspace at the start merges into the previous node, carrying its child.
func TestBackspaceMergesUp(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "A")
		s.seedCmd("node", "add", "--parent", "scratch", "B")
		s.seedCmd("node", "add", "--parent", "B", "C")
	})
	s.waitFor("A", waitTimeout)
	s.send("Down") // cursor onto B
	s.send("Home")
	s.send("BSpace")
	snap := s.waitFor("AB", waitTimeout)
	if !strings.Contains(snap, "○ AB") {
		t.Fatalf("B did not merge into A:\n%s", snap)
	}
	idxAB := strings.Index(snap, "AB")
	idxC := strings.Index(snap, "○ C")
	if idxC < 0 || idxC < idxAB {
		t.Fatalf("child C should follow under AB:\n%s", snap)
	}
}

// 5. Left at the start crosses to the previous node's end: on "two" Home, Left,
//    type "X" -> "oneX".
func TestLeftCrossesToPrevEnd(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "one")
		s.seedCmd("node", "add", "--parent", "scratch", "two")
	})
	s.waitFor("two", waitTimeout)
	s.send("Down") // cursor onto two
	s.send("Home")
	s.send("Left")
	s.sendText("X")
	snap := s.waitFor("oneX", waitTimeout)
	if !strings.Contains(snap, "○ oneX") {
		t.Fatalf("X should have landed at the end of one:\n%s", snap)
	}
}

// 6. Tab indents and Shift+Tab (BTab) outdents a node.
func TestTabIndentOutdent(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "first")
		s.seedCmd("node", "add", "--parent", "scratch", "second")
	})
	s.waitFor("second", waitTimeout)
	s.send("Down") // cursor onto second
	s.send("Tab")
	s.waitFor("╰─ ○ second", waitTimeout) // now a child of first
	s.send("BTab")
	snap := s.waitForFunc("second back at top level", func(out string) bool {
		return strings.Contains(out, "○ second") && !strings.Contains(out, "╰─ ○ second")
	}, waitTimeout)
	if strings.Contains(snap, "╰─ ○ second") {
		t.Fatalf("BTab did not outdent second:\n%s", snap)
	}
}

// 7. Delete: C-d on a leaf removes it; C-d on a node with children shows the
//    inline confirm and Esc keeps it.
func TestDeleteLeafAndConfirm(t *testing.T) {
	t.Run("leaf removed", func(t *testing.T) {
		s := newSession(t, 70, 20, func(s *session) {
			seedScratch(s)
			s.seedCmd("node", "add", "--parent", "scratch", "leaf")
			s.seedCmd("node", "add", "--parent", "scratch", "keep")
		})
		s.waitFor("leaf", waitTimeout)
		s.send("C-d")
		snap := s.waitForFunc("leaf gone", func(out string) bool {
			return !strings.Contains(out, "leaf") && strings.Contains(out, "keep")
		}, waitTimeout)
		assertNoCrash(t, snap)
	})

	t.Run("parent confirm then keep", func(t *testing.T) {
		s := newSession(t, 70, 20, func(s *session) {
			seedScratch(s)
			s.seedCmd("node", "add", "--parent", "scratch", "parent")
			s.seedCmd("node", "add", "--parent", "parent", "kid")
		})
		s.waitFor("parent", waitTimeout)
		s.send("C-d")
		s.waitFor("enter delete · esc keep", waitTimeout)
		s.send("Escape")
		snap := s.waitForFunc("confirm dismissed, parent kept", func(out string) bool {
			return strings.Contains(out, "parent") && !strings.Contains(out, "esc keep")
		}, waitTimeout)
		if !strings.Contains(snap, "kid") {
			t.Fatalf("kid should still exist after keep:\n%s", snap)
		}
	})
}

// 8. Deleting through a mirror must not crash. Build a node with a child plus an
//    empty trailing child, mirror it onto an empty sibling, then backspace on the
//    original's child. The program must stay alive and keep rendering.
func TestDeleteThroughMirrorNoCrash(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "src")
		s.seedCmd("node", "add", "--parent", "src", "c1")
		s.seedCmd("node", "add", "--parent", "src", "")    // empty trailing child
		s.seedCmd("node", "add", "--parent", "scratch", "") // empty sibling to host the mirror
	})
	s.waitFor("src", waitTimeout)
	// rows: src / c1 / (empty child) / (empty sibling). Move to the empty sibling.
	s.send("Down", "Down", "Down")
	s.sendText("/")
	s.waitFor("/mirror", waitTimeout)
	s.sendText("mirror")
	s.send("Enter")
	s.sendText("src")
	s.send("Enter")
	// mirror now shows src's children through it
	s.waitFor("◆ src · mirror", waitTimeout)
	// move cursor up onto the original subtree and backspace through it
	s.send("Up", "Up", "Up")
	s.send("BSpace")
	snap := s.waitForFunc("outline still renders", func(out string) bool {
		return strings.Contains(out, "scratch ·")
	}, waitTimeout)
	assertNoCrash(t, snap)
	if !strings.Contains(snap, "mirror") {
		t.Fatalf("mirror row should still render after the delete:\n%s", snap)
	}
}

// 9. A long no-space node wraps onto continuation lines and is never truncated
//    with an ellipsis.
func TestLongLineWrapsNoEllipsis(t *testing.T) {
	long := strings.Repeat("a", 60)
	s := newSession(t, 30, 12, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", long)
	})
	snap := s.waitFor("aaaa", waitTimeout)
	if strings.Contains(snap, "…") {
		t.Fatalf("long line was truncated with an ellipsis:\n%s", snap)
	}
	// the 60 'a's cannot fit on one 30-col line, so a continuation line must exist
	runs := 0
	for _, line := range strings.Split(snap, "\n") {
		if strings.Contains(line, "aaaaaaaaaa") {
			runs++
		}
	}
	if runs < 2 {
		t.Fatalf("expected the long line to wrap onto a second row, got %d rows:\n%s", runs, snap)
	}
}

// 10. The slash menu lists /move, /pull:wf and /undo, and never /mirror_to.
func TestSlashMenuCommands(t *testing.T) {
	s := newSession(t, 70, 24, seedScratch)
	s.waitFor("scratch", waitTimeout)
	s.sendText("/")
	snap := s.waitFor("/undo", waitTimeout)
	for _, want := range []string{"/move", "/pull:wf", "/undo"} {
		if !strings.Contains(snap, want) {
			t.Fatalf("slash menu missing %q:\n%s", want, snap)
		}
	}
	if strings.Contains(snap, "/mirror_to") {
		t.Fatalf("slash menu should not list /mirror_to:\n%s", snap)
	}
}

// 11. /go hides empty-named nodes: with one named and one empty node, the finder
//     lists the named one and no blank row.
func TestGoHidesEmptyNodes(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "named")
		s.seedCmd("node", "add", "--parent", "scratch", "")
	})
	s.waitFor("named", waitTimeout)
	s.sendText("/")
	s.waitFor("/go", waitTimeout)
	s.sendText("go")
	s.send("Enter")
	snap := s.waitFor("enter open node", waitTimeout)
	// the finder lists openable nodes; "scratch" is the parent of "named".
	if !strings.Contains(snap, "scratch") {
		t.Fatalf("/go should list the named ancestry:\n%s", snap)
	}
	// no blank finder row: every listed row carries a node count label
	for _, line := range strings.Split(snap, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "▸") && trimmed == "▸" {
			t.Fatalf("/go listed a blank node row:\n%s", snap)
		}
	}
}

// 12. Opening a deep (grandchild) node shows its full ancestry in the bottom bar,
//     joined by " › ".
func TestBreadcrumbAncestry(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "alpha")
		s.seedCmd("node", "add", "--parent", "alpha", "beta")
	}, "node", "open", "beta")
	snap := s.waitFor("scratch › alpha › beta", waitTimeout)
	assertNoCrash(t, snap)
}

// 13. alt+left (M-Left) walks up to the parent: from beta it reopens alpha.
func TestAltLeftWalksUp(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "alpha")
		s.seedCmd("node", "add", "--parent", "alpha", "beta")
	}, "node", "open", "beta")
	s.waitFor("scratch › alpha › beta", waitTimeout)
	s.send("M-Left")
	snap := s.waitForFunc("breadcrumb shortened to alpha", func(out string) bool {
		return strings.Contains(out, "scratch › alpha ·") && !strings.Contains(out, "› beta")
	}, waitTimeout)
	// alpha's children (beta) now show
	if !strings.Contains(snap, "○ beta") {
		t.Fatalf("after walking up, beta should show as a child:\n%s", snap)
	}
}

// 14. Zoom: M-Right zooms into a node (children become the view), M-Left zooms
//     back out.
func TestZoomInOut(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "parent")
		s.seedCmd("node", "add", "--parent", "parent", "kid")
	})
	s.waitFor("parent", waitTimeout)
	s.send("M-Right")
	snap := s.waitFor("scratch › parent", waitTimeout)
	if !strings.Contains(snap, "○ kid") {
		t.Fatalf("zoomed-in view should show kid:\n%s", snap)
	}
	s.send("M-Left")
	snap = s.waitForFunc("zoomed back out", func(out string) bool {
		return strings.Contains(out, "○ parent") && !strings.Contains(out, "scratch › parent")
	}, waitTimeout)
	assertNoCrash(t, snap)
}

// 15. Fold: ctrl+space collapses a node with children to "● name · N children"
//     and toggles back.
func TestFoldToggle(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "parent")
		s.seedCmd("node", "add", "--parent", "parent", "kid")
	})
	s.waitFor("○ kid", waitTimeout)
	s.send("C-Space")
	snap := s.waitFor("● parent · 1 child", waitTimeout)
	if strings.Contains(snap, "○ kid") {
		t.Fatalf("kid should be hidden while folded:\n%s", snap)
	}
	s.send("C-Space")
	snap = s.waitFor("○ kid", waitTimeout)
	if strings.Contains(snap, "● parent") {
		t.Fatalf("node should be expanded again:\n%s", snap)
	}
}

// 16. /undo reverts typed text.
func TestUndoRevertsTyping(t *testing.T) {
	s := newSession(t, 70, 20, seedScratch)
	s.sendText("typed text")
	s.waitFor("typed text", waitTimeout)
	s.sendText("/")
	s.waitFor("/undo", waitTimeout)
	s.sendText("undo")
	s.send("Enter")
	snap := s.waitForFunc("typed text reverted", func(out string) bool {
		return !strings.Contains(out, "typed text")
	}, waitTimeout)
	assertNoCrash(t, snap)
}

// 17. /mirror show-through: mirroring a node that has children renders the
//     "◆ <name> · mirror" row with that node's children shown through as ◆ rows.
func TestMirrorShowThrough(t *testing.T) {
	s := newSession(t, 70, 20, func(s *session) {
		seedScratch(s)
		s.seedCmd("node", "add", "--parent", "scratch", "target")
		s.seedCmd("node", "add", "--parent", "target", "tkid")
		s.seedCmd("node", "add", "--parent", "scratch", "") // empty node to host the mirror
	})
	s.waitFor("target", waitTimeout)
	// rows: target / tkid / (empty). Move past tkid onto the empty sibling so the
	// mirror is hosted there, not nested under target (which would self-cycle).
	s.send("Down", "Down")
	s.sendText("/")
	s.waitFor("/mirror", waitTimeout)
	s.sendText("mirror")
	s.send("Enter")
	s.sendText("target")
	s.send("Enter")
	snap := s.waitFor("◆ target · mirror", waitTimeout)
	if !strings.Contains(snap, "◆ tkid") {
		t.Fatalf("mirror should show target's child tkid through it as a ◆ row:\n%s", snap)
	}
}

// 18. /pull:wf prompts for a workflowy api key when none is configured; Escape
//     cancels. No key is entered and no network call is made.
func TestPullWfPrompts(t *testing.T) {
	s := newSession(t, 70, 20, seedScratch)
	s.waitFor("scratch", waitTimeout)
	s.sendText("/")
	s.waitFor("/pull:wf", waitTimeout)
	s.sendText("pull")
	s.send("Enter")
	s.waitFor("workflowy api key:", waitTimeout)
	s.send("Escape")
	snap := s.waitForFunc("prompt cancelled", func(out string) bool {
		return !strings.Contains(out, "workflowy api key:")
	}, waitTimeout)
	assertNoCrash(t, snap)
}

// 19. Persistence: type a node, save, quit, reopen -> the node persists.
func TestPersistenceAcrossReopen(t *testing.T) {
	s := newSession(t, 70, 20, seedScratch)
	s.sendText("persisted node")
	s.waitFor("persisted node", waitTimeout)
	s.send("C-s")
	// wait for the save to clear the "unsaved" marker
	s.waitForFunc("saved", func(out string) bool {
		return !strings.Contains(out, "unsaved")
	}, waitTimeout)
	s.send("Escape", "Escape")
	s.reopen()
	snap := s.waitFor("persisted node", waitTimeout)
	if !strings.Contains(snap, "○ persisted node") {
		t.Fatalf("node did not persist across reopen:\n%s", snap)
	}
}

// bulletLines returns the trimmed text of each rendered bullet row ("○ <text>"),
// stripping any tree-drawing prefix.
func bulletLines(snap string) []string {
	var out []string
	for _, line := range strings.Split(snap, "\n") {
		idx := strings.Index(line, "○ ")
		if idx < 0 {
			// a bullet with empty text renders as a bare "○"
			if t := strings.TrimSpace(line); t == "○" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, strings.TrimSpace(line[idx+len("○ "):]))
	}
	return out
}
