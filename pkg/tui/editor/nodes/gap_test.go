package nodes

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

func TestCanvasGapAdjustKey(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "cv3", typ: database.TypeCanvas}
	v := canvasView{}
	v.Enter(h, n)
	st := canvasStateOf(h, "cv3")
	st.doc.Objects = []canvasCell{{X: 1, Y: 1, Ch: "█", Fg: "red"}, {X: 8, Y: 1, Ch: "█", Fg: "blue"}}
	st.doc.Ties = []canvasTie{{From: canvasEnd{Obj: "blue", Edge: "left"}, To: canvasEnd{Obj: "red", Edge: "right"}, Gap: 7}}
	st.tieSel = 0
	if _, handled := v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")}); !handled {
		t.Fatal("+ must be handled")
	}
	if st.doc.Ties[0].Gap != 8 {
		t.Fatalf("gap = %d, want 8", st.doc.Ties[0].Gap)
	}
	bl, _ := st.doc.edgeCoord(canvasEnd{Obj: "blue", Edge: "left"})
	if bl != 9 {
		t.Fatalf("blue must move to 9, got %d", bl)
	}
}
