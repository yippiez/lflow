package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

func circReset() {
	circGrids = map[string][]byte{}
	circItems = map[string][]bool{}
	circScores = map[string]int{}
	circCursors = map[string]int{}
	circLastDir = map[string]byte{}
	circFocus = ""
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

// board/items helpers for step tests (lane per row).
func lanesOf(rows ...string) [][]byte {
	g := make([][]byte, len(rows))
	for i, r := range rows {
		g[i] = []byte(r)
	}
	return g
}

func itemsAt(g [][]byte, at ...[2]int) [][]bool {
	it := make([][]bool, len(g))
	for l := range g {
		it[l] = make([]bool, len(g[l]))
	}
	for _, p := range at {
		it[p[0]][p[1]] = true
	}
	return it
}

// TestCircBeltCarries: an item rides a belt one tile per beat and a whole
// convoy moves as one — no gaps opening between queued items.
func TestCircBeltCarries(t *testing.T) {
	g := lanesOf(">>>>")
	it := itemsAt(g, [2]int{0, 0}, [2]int{0, 1})
	circStepBoard(g, it, 1)
	if it[0][0] || !it[0][1] || !it[0][2] {
		t.Fatalf("convoy must advance as one: %v", it[0])
	}
}

// TestCircBackpressure: a jammed line holds — the front item has nowhere to
// go (belt end), so the follower waits and nothing falls off the board.
func TestCircBackpressure(t *testing.T) {
	g := lanesOf(">>")
	it := itemsAt(g, [2]int{0, 0}, [2]int{0, 1})
	circStepBoard(g, it, 1)
	if !it[0][0] || !it[0][1] {
		t.Fatalf("jammed belt must hold both items: %v", it[0])
	}
}

// TestCircCoreCollects: an item reaching a core is consumed and scored to the
// core's lane.
func TestCircCoreCollects(t *testing.T) {
	g := lanesOf(">>O")
	it := itemsAt(g, [2]int{0, 1})
	scored := circStepBoard(g, it, 1)
	if it[0][1] || it[0][2] {
		t.Fatalf("the core must consume the item: %v", it[0])
	}
	if scored[0] != 1 {
		t.Fatalf("scored = %v, want one on lane 0", scored)
	}
}

// TestCircDrillEmits: on its beat a drill fills free neighboring belts —
// its own lane and the lanes above/below; off the beat it stays quiet.
func TestCircDrillEmits(t *testing.T) {
	g := lanesOf(
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

// TestCircSeamCarries: contiguous circuit siblings fuse lanes — a v belt
// hands its item to the sibling lane below, where a core collects it.
func TestCircSeamCarries(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit, database.TypeCircuit)
	top, bottom := circGridOf(h, "a"), circGridOf(h, "b")
	top[3], top[4], top[5] = tileBeltR, tileBeltR, tileBeltD
	bottom[5], bottom[6], bottom[7] = tileBeltR, tileBeltR, tileCore

	cmd := circToggle(h, kids[0])
	if cmd == nil {
		t.Fatalf("run must start: %s", h.flash)
	}
	if circRuns["a"] == nil || circRuns["b"] == nil {
		t.Fatal("both stack members must join the run")
	}
	circItemsOf("a")[4] = true // an item heading for the down-belt
	r := circRuns["a"]
	circTickMsg{run: r}.HandleNodePlugin(h) // → onto the v
	circTickMsg{run: r}.HandleNodePlugin(h) // → across the seam
	if !circItemsOf("b")[5] {
		t.Fatal("the v belt must hand the item to the lane below")
	}
	for i := 0; i < 4; i++ {
		circTickMsg{run: r}.HandleNodePlugin(h)
	}
	if circScores["b"] == 0 {
		t.Fatal("the lower lane's core must collect and score")
	}
	// stop restores drawings and sweeps the items
	circToggle(h, kids[0])
	if circRuns["a"] != nil || circRuns["b"] != nil {
		t.Fatal("stop must clear the run")
	}
	for _, has := range circItemsOf("a") {
		if has {
			t.Fatal("stop must sweep the items")
		}
	}
}

// TestCircStackContiguity: a non-circuit sibling splits the board.
func TestCircStackContiguity(t *testing.T) {
	kids := circTestStack(database.TypeCircuit, database.TypeBullets, database.TypeCircuit)
	if got := len(circStack(kids[0])); got != 1 {
		t.Fatalf("stack across a bullet = %d lanes, want 1", got)
	}
}

// TestCircInlineBuilder: the builder is the row itself — belts lay along the
// cursor's drag, r spins, d/o place machines, x erases; leaving persists and
// the view never asks for band lines (no expansion).
func TestCircInlineBuilder(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit)
	n := kids[0]
	v := circView{}
	if !v.Enter(h, n) {
		t.Fatal("builder must focus")
	}
	if v.Lines(h, n, 80) != 0 {
		t.Fatal("the inline builder must not hang band lines")
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRight}) // drag right…
	v.Key(h, n, tea.KeyMsg{Type: tea.KeySpace}) // …and lay a belt flowing right
	cur := circCursors["a"]
	g := circGridOf(h, "a")
	if g[cur] != tileBeltR {
		t.Fatalf("belt = %c, want > after a rightward drag", g[cur])
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}) // spin clockwise
	if g[cur] != tileBeltD {
		t.Fatalf("spun belt = %c, want v", g[cur])
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if g[cur] != tileDrill {
		t.Fatal("d must place a drill")
	}
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if g[cur] != tileCore {
		t.Fatal("o must place a core")
	}
	// up/down stay with the outline — the builder must not swallow them
	if _, handled := v.Key(h, n, tea.KeyMsg{Type: tea.KeyUp}); handled {
		t.Fatal("↑ must fall through to the outline")
	}
	v.Leave(h, n)
	delete(circGrids, "a")
	if g2 := circGridOf(h, "a"); g2[cur] != tileCore {
		t.Fatalf("leave must persist the lane: %c", g2[cur])
	}
}

// TestCircSaveLoadRoundtrip: the lane persists to node_output and reloads
// tile-perfect after the live state is dropped (an editor restart).
func TestCircSaveLoadRoundtrip(t *testing.T) {
	h := newFakeHost(t)
	circTestStack(database.TypeCircuit)
	g := circGridOf(h, "a")
	g[3], g[4], g[5], g[6] = tileDrill, tileBeltR, tileBeltD, tileCore
	circSave(h, "a")
	delete(circGrids, "a")
	g2 := circGridOf(h, "a")
	if g2[3] != tileDrill || g2[4] != tileBeltR || g2[5] != tileBeltD || g2[6] != tileCore {
		t.Fatalf("reloaded tiles = %c %c %c %c", g2[3], g2[4], g2[5], g2[6])
	}
	if g2[0] != tileEmpty {
		t.Fatal("untouched tiles must stay floor")
	}
}

// TestCircInlineRender: the whole machine lives in the row body — the strip's
// half-block pixels, the checkered floor, the ⌥r hint idle and the yellow
// tally while live. No bands, no expansion.
func TestCircInlineRender(t *testing.T) {
	h := newFakeHost(t)
	kids := circTestStack(database.TypeCircuit)
	n := kids[0]
	body := circRender(h, n)
	if !strings.Contains(body, "▀") || !strings.Contains(body, "\x1b[48;2;") {
		t.Fatalf("the body must be the half-block strip: %q", body[:60])
	}
	if !strings.Contains(body, "13;19;33") || !strings.Contains(body, "17;24;41") {
		t.Fatalf("floor must checker: %q", body[:120])
	}
	if !strings.Contains(body, "⌥r run") {
		t.Fatalf("idle tail must hint the run: %q", body)
	}
	circToggle(h, n)
	circScores["a"] = 7
	if body := circRender(h, n); !strings.Contains(body, "live 7") {
		t.Fatalf("live tail must tally: %q", body)
	}
	// the builder cursor shows only while the lane is focused and stopped
	circStopRun(circRuns["a"])
	circFocus, circCursors["a"] = "a", 2
	if body := circRender(h, n); !strings.Contains(body, "245;245;245") {
		t.Fatalf("focused body must show the cursor tile: %q", body)
	}
}
