package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

func circReset() {
	circGrids = map[string][][]byte{}
	circItems = map[string][][]bool{}
	circScores = map[string]int{}
	circCursors = map[string][2]int{}
	circLastDir = map[string]byte{}
	circRuns = map[string]*circRun{}
}

// circTestStack hangs kids of the given types under one parent.
func circTestStack(types ...string) []*fakeNode {
	circReset()
	parent := &fakeNode{uuid: "p", typ: database.TypeBullets}
	for i, typ := range types {
		k := &fakeNode{uuid: string(rune('a' + i)), typ: typ, parent: parent}
		parent.kids = append(parent.kids, k)
	}
	return parent.kids
}

// board/items helpers for step tests.
func rowsOf(rows ...string) [][]byte {
	g := make([][]byte, len(rows))
	for i, r := range rows {
		g[i] = []byte(r)
	}
	return g
}

func itemsAt(g [][]byte, at ...[2]int) [][]bool {
	it := make([][]bool, len(g))
	for y := range g {
		it[y] = make([]bool, len(g[y]))
	}
	for _, p := range at {
		it[p[0]][p[1]] = true
	}
	return it
}

// TestCircBeltCarries: an item rides a belt one tile per beat and a whole
// convoy moves as one — no gaps opening between queued items.
func TestCircBeltCarries(t *testing.T) {
	g := rowsOf(">>>>")
	it := itemsAt(g, [2]int{0, 0}, [2]int{0, 1})
	circStepBoard(g, it, 1)
	if it[0][0] || !it[0][1] || !it[0][2] {
		t.Fatalf("convoy must advance as one: %v", it[0])
	}
}

// TestCircBackpressure: a jammed line holds — the front item has nowhere to
// go (belt end), so the follower waits and nothing falls off the board.
func TestCircBackpressure(t *testing.T) {
	g := rowsOf(">>")
	it := itemsAt(g, [2]int{0, 0}, [2]int{0, 1})
	circStepBoard(g, it, 1)
	if !it[0][0] || !it[0][1] {
		t.Fatalf("jammed belt must hold both items: %v", it[0])
	}
}

// TestCircCoreCollects: an item reaching a core is consumed and scored to the
// core's board row.
func TestCircCoreCollects(t *testing.T) {
	g := rowsOf(">>O")
	it := itemsAt(g, [2]int{0, 1})
	scored := circStepBoard(g, it, 1)
	if it[0][1] || it[0][2] {
		t.Fatalf("the core must consume the item: %v", it[0])
	}
	if scored[0] != 1 {
		t.Fatalf("scored = %v, want one at row 0", scored)
	}
}

// TestCircDrillEmits: on its beat a drill fills free neighboring belts; off
// the beat it stays quiet.
func TestCircDrillEmits(t *testing.T) {
	g := rowsOf(
		".v.",
		">D>",
	)
	it := itemsAt(g)
	circStepBoard(g, it, 1) // beat 1: not a drill beat
	if it[0][1] || it[1][0] || it[1][2] {
		t.Fatal("no emission off the drill beat")
	}
	circStepBoard(g, it, circDrillEvery) // a drill beat
	if !it[0][1] || !it[1][0] || !it[1][2] {
		t.Fatalf("drill must fill its free neighbor belts: %v", it)
	}
}

// TestCircSeamCarries: contiguous circuit siblings fuse floors — a down-belt
// line carries an item across the node seam into the lower canvas.
func TestCircSeamCarries(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit, database.TypeCircuit)
	top, bottom := circGridOf(h, "a"), circGridOf(h, "b")
	for y := 0; y < tileH; y++ {
		top[y][5] = tileBeltD
		bottom[y][5] = tileBeltD
	}
	bottom[tileH-1][5] = tileCore

	cmd := circToggle(h, kids[0])
	if cmd == nil {
		t.Fatalf("run must start: %s", h.flash)
	}
	if circRuns["a"] == nil || circRuns["b"] == nil {
		t.Fatal("both stack members must join the run")
	}
	circItemsOf("a")[tileH-1][5] = true // an item at the seam's upper lip
	r := circRuns["a"]
	circTickMsg{run: r}.HandleNodePlugin(h)
	if !circItemsOf("b")[0][5] {
		t.Fatal("the item must cross the node seam onto the lower floor")
	}
	// ride it down to the core — the lower node's tally takes the score
	for i := 0; i < tileH; i++ {
		circTickMsg{run: r}.HandleNodePlugin(h)
	}
	if circScores["b"] == 0 {
		t.Fatal("the lower node's core must collect and score")
	}
	// stop restores drawings and clears the floor of items
	circToggle(h, kids[0])
	if circRuns["a"] != nil || circRuns["b"] != nil {
		t.Fatal("stop must clear the run")
	}
	for y := range circItemsOf("a") {
		for x := range circItemsOf("a")[y] {
			if circItemsOf("a")[y][x] {
				t.Fatal("stop must sweep the items")
			}
		}
	}
}

// TestCircStackContiguity: a non-circuit sibling splits the board.
func TestCircStackContiguity(t *testing.T) {
	kids := circTestStack(database.TypeCircuit, database.TypeBullets, database.TypeCircuit)
	if got := len(circStack(kids[0])); got != 1 {
		t.Fatalf("stack across a bullet = %d members, want 1", got)
	}
}

// TestCircBuilder: belts lay along the cursor's drag direction, r spins them,
// d/o place drill and core, x erases, and leave persists the floor.
func TestCircBuilder(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit)
	n := kids[0]
	v := circView{}
	if !v.Enter(h, n) {
		t.Fatal("builder must open")
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRight}) // drag right…
	v.Key(h, n, tea.KeyMsg{Type: tea.KeySpace}) // …and lay a belt flowing right
	cur := circCursors["a"]
	g := circGridOf(h, "a")
	if g[cur[1]][cur[0]] != tileBeltR {
		t.Fatalf("belt = %c, want > after a rightward drag", g[cur[1]][cur[0]])
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}) // spin clockwise
	if g[cur[1]][cur[0]] != tileBeltD {
		t.Fatalf("spun belt = %c, want v", g[cur[1]][cur[0]])
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyDown}) // drag down: lastDir follows
	v.Key(h, n, tea.KeyMsg{Type: tea.KeySpace})
	cur = circCursors["a"]
	if g[cur[1]][cur[0]] != tileBeltD {
		t.Fatal("belts lay along the drag direction")
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if g[cur[1]][cur[0]] != tileDrill {
		t.Fatal("d must place a drill")
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if g[cur[1]][cur[0]] != tileCore {
		t.Fatal("o must place a core")
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if g[cur[1]][cur[0]] != tileEmpty {
		t.Fatal("x must erase")
	}
	v.Leave(h, n)
	spun := [2]int{cur[0], cur[1] - 1} // the belt spun to v before the drag down
	delete(circGrids, "a")
	if g2 := circGridOf(h, "a"); g2[spun[1]][spun[0]] != tileBeltD {
		t.Fatalf("leave must persist the floor: %c", g2[spun[1]][spun[0]])
	}
}

// TestCircSaveLoadRoundtrip: the floor persists to node_output and reloads
// tile-perfect after the live state is dropped (an editor restart).
func TestCircSaveLoadRoundtrip(t *testing.T) {
	h := newFakeHost(t)
	circTestStack(database.TypeCircuit)
	g := circGridOf(h, "a")
	g[2][3], g[2][4], g[2][5], g[3][3] = tileDrill, tileBeltR, tileCore, tileBeltU
	circSave(h, "a")
	delete(circGrids, "a")
	g2 := circGridOf(h, "a")
	if g2[2][3] != tileDrill || g2[2][4] != tileBeltR || g2[2][5] != tileCore || g2[3][3] != tileBeltU {
		t.Fatalf("reloaded tiles = %c %c %c %c", g2[2][3], g2[2][4], g2[2][5], g2[3][3])
	}
	if g2[0][0] != tileEmpty {
		t.Fatal("untouched tiles must stay floor")
	}
}

// TestCircPreviewTiles: the closed row hangs the tile preview — one text row
// per tile row, half-block pixels on the checkered floor.
func TestCircPreviewTiles(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit)
	lines := circPreview(h, kids[0], "", 200, false)
	if len(lines) != tileH {
		t.Fatalf("preview rows = %d, want %d", len(lines), tileH)
	}
	if !strings.Contains(lines[0], "▀") || !strings.Contains(lines[0], "\x1b[48;2;") {
		t.Fatalf("preview must be half-block pixels: %q", lines[0][:60])
	}
	// the checker: two floor shades alternate along the row
	if !strings.Contains(lines[0], "13;19;33") || !strings.Contains(lines[0], "17;24;41") {
		t.Fatalf("floor must checker: %q", lines[0][:120])
	}
	if circPreview(h, kids[0], "", 200, true) != nil {
		t.Fatal("the builder's bands take over while focused")
	}
}
