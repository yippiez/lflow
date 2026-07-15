package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

func circReset() {
	circGrids = map[string][][]byte{}
	circCursors = map[string][2]int{}
	circRuns = map[string]*circRun{}
}

// circTestStack hangs circuit kids (plus optional other types) under a parent.
func circTestStack(types ...string) []*fakeNode {
	circReset()
	parent := &fakeNode{uuid: "p", typ: database.TypeBullets}
	for i, typ := range types {
		k := &fakeNode{uuid: string(rune('a' + i)), typ: typ, parent: parent}
		parent.kids = append(parent.kids, k)
	}
	return parent.kids
}

// TestCircStepRules: the Wireworld generation — head cools to tail, tail back
// to conductor, a conductor fires on 1–2 neighboring heads and holds on more.
func TestCircStepRules(t *testing.T) {
	g := [][]byte{
		[]byte("THC."),
		[]byte("...."),
	}
	out := circStep(g)
	if out[0][0] != circCond || out[0][1] != circTail {
		t.Fatalf("H→T, T→C broken: %s", out[0])
	}
	if out[0][2] != circHead {
		t.Fatal("a conductor beside one head must fire")
	}
	// three heads around a conductor → it holds
	g = [][]byte{
		[]byte("HHH"),
		[]byte(".C."),
	}
	if out := circStep(g); out[1][1] != circCond {
		t.Fatal("a conductor beside three heads must hold")
	}
}

// TestCircElectronTravelsWire: an electron runs down a straight track and
// leaves the far end without residue.
func TestCircElectronTravelsWire(t *testing.T) {
	g := [][]byte{[]byte("THCCCC")}
	for i := 0; i < 4; i++ {
		g = circStep(g)
	}
	if g[0][5] != circHead {
		t.Fatalf("electron must reach the wire end: %s", g[0])
	}
	g = circStep(g)
	g = circStep(g)
	if strings.ContainsAny(string(g[0]), "HT") {
		t.Fatalf("electron must leave the board: %s", g[0])
	}
}

// TestCircSaveLoadRoundtrip: the drawing persists to node_output and reloads
// cell-perfect after the live canvas is dropped (an editor restart).
func TestCircSaveLoadRoundtrip(t *testing.T) {
	h := newFakeHost(t)
	circTestStack(database.TypeCircuit)
	g := circGridOf(h, "a")
	g[3][7], g[3][8], g[4][7] = circCond, circHead, circTail
	circSave(h, "a")
	delete(circGrids, "a") // restart: live canvases are ephemeral
	g2 := circGridOf(h, "a")
	if g2[3][7] != circCond || g2[3][8] != circHead || g2[4][7] != circTail {
		t.Fatalf("reloaded cells = %c %c %c", g2[3][7], g2[3][8], g2[4][7])
	}
	if g2[0][0] != circEmpty {
		t.Fatal("untouched cells must stay empty")
	}
}

// TestCircStackFuses: contiguous circuit siblings simulate as ONE board — an
// electron aimed at the bottom seam of the first node crosses into the second.
func TestCircStackFuses(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit, database.TypeCircuit)
	top, bottom := circGridOf(h, "a"), circGridOf(h, "b")
	// a vertical track spanning the seam, spark at its top
	for y := 6; y < circH; y++ {
		top[y][5] = circCond
	}
	for y := 0; y < 4; y++ {
		bottom[y][5] = circCond
	}
	top[6][5], top[7][5] = circTail, circHead

	cmd := circToggle(h, kids[0])
	if cmd == nil {
		t.Fatalf("run must start: %s", h.flash)
	}
	if circRuns["a"] == nil || circRuns["b"] == nil {
		t.Fatal("both stack members must join the run")
	}
	// step the fused board a few generations by hand
	r := circRuns["a"]
	for i := 0; i < 5; i++ {
		circTickMsg{run: r}.HandleNodePlugin(h)
	}
	if !strings.ContainsAny(string(circGrids["b"][0])+string(circGrids["b"][1])+string(circGrids["b"][2]), "HT") {
		t.Fatal("the electron must cross the node seam into the lower canvas")
	}

	// stop restores both drawings (the spark back at its seed)
	circToggle(h, kids[0])
	if circRuns["a"] != nil || circRuns["b"] != nil {
		t.Fatal("stop must clear the run")
	}
	if circGrids["a"][7][5] != circHead {
		t.Fatal("stop must restore the drawing")
	}
}

// TestCircStackContiguity: a non-circuit sibling splits the board.
func TestCircStackContiguity(t *testing.T) {
	kids := circTestStack(database.TypeCircuit, database.TypeBullets, database.TypeCircuit)
	if got := len(circStack(kids[0])); got != 1 {
		t.Fatalf("stack across a bullet = %d members, want 1", got)
	}
}

// TestCircPainter: the crosshair paints conductor/electron/erase and the
// drawing lands in node_output on leave.
func TestCircPainter(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit)
	n := kids[0]
	v := circView{}
	if !v.Enter(h, n) {
		t.Fatal("painter must open")
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeySpace}) // conductor under the cursor
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRight})
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}}) // electron
	cur := circCursors["a"]
	g := circGridOf(h, "a")
	if g[cur[1]][cur[0]-1] != circCond || g[cur[1]][cur[0]] != circHead {
		t.Fatalf("painted cells = %c %c", g[cur[1]][cur[0]-1], g[cur[1]][cur[0]])
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if g[cur[1]][cur[0]] != circEmpty {
		t.Fatal("x must erase")
	}
	v.Leave(h, n)
	delete(circGrids, "a")
	if g2 := circGridOf(h, "a"); g2[cur[1]][cur[0]-1] != circCond {
		t.Fatal("leave must persist the drawing")
	}
}

// TestCircPreviewPixels: the closed row hangs the pixelated preview — one text
// row per two cell rows, painted with the board palette.
func TestCircPreviewPixels(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit)
	lines := circPreview(h, kids[0], "", 200, false)
	if len(lines) != circH/2 {
		t.Fatalf("preview rows = %d, want %d", len(lines), circH/2)
	}
	if !strings.Contains(lines[0], "▀") || !strings.Contains(lines[0], "\x1b[48;2;15;22;38m") {
		t.Fatalf("preview must be half-block pixels on the navy board: %q", lines[0][:80])
	}
	if circPreview(h, kids[0], "", 200, true) != nil {
		t.Fatal("the painter's bands take over while focused")
	}
}
