package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/tui/database"
)

func mouseZoneFor(t *testing.T, m *Model, action string, row int) mouseZone {
	t.Helper()
	for _, zone := range m.mouseFrame.zones {
		if zone.action == action && zone.row == row {
			return zone
		}
	}
	t.Fatalf("no %s zone for row %d: %+v", action, row, m.mouseFrame.zones)
	return mouseZone{}
}

func TestMouseFrameClickPlacesCaretOnWrappedText(t *testing.T) {
	m := newTestModel(18, "alpha beta gamma delta")
	m.View()
	if len(m.mouseFrame.lines) < 2 || m.mouseFrame.lines[1].row != 0 {
		t.Fatalf("wrapped frame did not retain row ownership: %+v", m.mouseFrame.lines)
	}
	h := m.mouseFrame.lines[1]
	m.handleFrameMouse(tea.MouseMsg{X: h.textStart + 2, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.cursor != 0 || m.caret <= h.runeStart {
		t.Fatalf("click landed at row=%d caret=%d, line starts at %d", m.cursor, m.caret, h.runeStart)
	}
}

func TestPlainNodeIndicatorZooms(t *testing.T) {
	m := newTestModel(60, "plain")
	m.View()
	zoom := mouseZoneFor(t, m, "zoom", 0)
	if zoom.hi-zoom.lo != 1 {
		t.Fatalf("zoom zone = %+v, want the rendered indicator cell", zoom)
	}
	for _, zone := range m.mouseFrame.zones {
		if zone.row == 0 && zone.action == "run" {
			t.Fatal("a non-runnable node grew a run zone")
		}
	}
	m.handleFrameMouse(tea.MouseMsg{X: zoom.lo, Y: zoom.line + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.viewStack) != 2 || m.viewRoot().name != "plain" {
		t.Fatalf("indicator did not use universal zoom: stack=%v", len(m.viewStack))
	}
}

func TestRunnableNodeIndicatorRuns(t *testing.T) {
	ensureTypes()
	old := byType[database.TypeBash]
	runs := 0
	stub := old
	stub.run = func(*Model, *item) tea.Cmd { runs++; return nil }
	stub.flashActions = nil
	byType[database.TypeBash] = stub
	t.Cleanup(func() { byType[database.TypeBash] = old })

	m := newTestModel(60, "command")
	m.rows[0].it.typ = database.TypeBash
	m.View()
	run := mouseZoneFor(t, m, "run", 0)
	if run.hi-run.lo != 1 {
		t.Fatalf("run zone is not the indicator: %+v", run)
	}
	m.handleFrameMouse(tea.MouseMsg{X: run.lo, Y: run.line + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if runs != 1 || len(m.viewStack) != 1 {
		t.Fatalf("run cell ran %d times and left zoom depth %d", runs, len(m.viewStack))
	}
}

func TestVisibleFrameDragCopiesAcrossRowsWithoutFurniture(t *testing.T) {
	clip := clipCapture(t)
	m := newTestModel(40, "first words", "second words")
	m.View()
	first, second := m.mouseFrame.lines[0], m.mouseFrame.lines[1]
	m.handleFrameMouse(tea.MouseMsg{X: first.textStart, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m.handleFrameMouse(tea.MouseMsg{X: second.textStart + len("second"), Y: 2, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m.handleFrameMouse(tea.MouseMsg{X: second.textStart + len("second"), Y: 2, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	got := clip()
	if !strings.HasPrefix(got, "first words\nsecond") {
		t.Fatalf("drag copied %q", got)
	}
	if strings.ContainsAny(got, "○●│├╰─") {
		t.Fatalf("drag copied outline furniture: %q", got)
	}
	if !m.mouseFrame.drag.on || m.mouseFrame.drag.live {
		t.Fatalf("released drag state = %+v", m.mouseFrame.drag)
	}
}

func TestBreadcrumbClickReturnsToRenderedAncestor(t *testing.T) {
	m := newTestModel(60, "parent")
	parent := m.rows[0].it
	child := &item{name: "child", parent: parent}
	parent.children = append(parent.children, child)
	m.viewStack = append(m.viewStack, parent, child)
	m.refreshRows()
	m.View()
	var crumb mouseZone
	found := false
	for _, zone := range m.mouseFrame.zones {
		if zone.action == "crumb" && zone.root == parent {
			crumb, found = zone, true
			break
		}
	}
	if !found {
		t.Fatalf("parent breadcrumb has no zone: %+v", m.mouseFrame.zones)
	}
	m.handleFrameMouse(tea.MouseMsg{X: crumb.lo, Y: crumb.line + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.viewRoot() != parent || len(m.viewStack) != 2 {
		t.Fatalf("breadcrumb left stack depth %d at %q", len(m.viewStack), m.viewRoot().name)
	}
}

func TestMousePreferenceIsGoneBecauseCaptureIsDefault(t *testing.T) {
	for _, def := range settingDefs {
		if def.key == "mouse" {
			t.Fatal("mouse capture remains opt-in")
		}
	}
}
