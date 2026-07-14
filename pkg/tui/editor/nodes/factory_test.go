package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The shared factory-family harness: build a belt line out of fakeNodes, run
// it through the real engine (bash), and pump the event messages the way the
// editor's Update would.

// facTestLine hangs kids of the given (typ, text) pairs under one parent and
// returns them; facStats is reset so tests never see each other's chips.
func facTestLine(pairs ...[2]string) []*fakeNode {
	facStats = map[string]*facStat{}
	parent := &fakeNode{uuid: "p", typ: database.TypeBullets}
	for i, p := range pairs {
		k := &fakeNode{uuid: string(rune('a' + i)), typ: p[0], text: p[1], parent: parent}
		parent.kids = append(parent.kids, k)
	}
	return parent.kids
}

// facPump drains a line run to completion, event by event.
func facPump(t *testing.T, h *fakeHost, cmd tea.Cmd) {
	t.Helper()
	for i := 0; cmd != nil; i++ {
		if i > 200 {
			t.Fatal("line run did not settle")
		}
		msg, ok := cmd().(facEvMsg)
		if !ok {
			t.Fatal("run must speak facEvMsg")
		}
		cmd = msg.HandleNodePlugin(h)
	}
}

// TestFacLineContiguous: the belt line is the maximal run of factory-typed
// siblings around the machine — bullets on either side split the belt.
func TestFacLineContiguous(t *testing.T) {
	kids := facTestLine(
		[2]string{database.TypeBullets, "note"},
		[2]string{database.TypeMiner, "date"},
		[2]string{database.TypeAssembler, "tr a-z A-Z"},
		[2]string{database.TypeChest, "box"},
		[2]string{database.TypeBullets, "note"},
		[2]string{database.TypeMiner, "another line"},
	)
	line := facLine(kids[2])
	if len(line) != 3 || line[0].UUID() != "b" || line[2].UUID() != "d" {
		t.Fatalf("line = %d nodes, want the miner→assembler→chest run", len(line))
	}
	if got := facLine(kids[5]); len(got) != 1 || got[0].UUID() != "f" {
		t.Fatalf("the trailing miner must be its own line, got %d", len(got))
	}
}

// TestFacLineDelivers: miner → assembler → chest moves a payload down the
// belt, transformed; every machine parks with its ok chip and the chest holds
// the result.
func TestFacLineDelivers(t *testing.T) {
	h := newFakeHost(t)
	kids := facTestLine(
		[2]string{database.TypeMiner, "printf 'iron ore'"},
		[2]string{database.TypeAssembler, "tr a-z A-Z"},
		[2]string{database.TypeChest, "box"},
	)
	cmd := facRun(h, kids[0])
	if cmd == nil {
		t.Fatalf("run must start: %s", h.flash)
	}
	if a, _ := h.NodeStore("a")["animating"].(bool); !a {
		t.Fatal("run must raise the launcher's animating flag")
	}
	facPump(t, h, cmd)

	if got := facStats["c"].payload; got != "IRON ORE" {
		t.Fatalf("chest holds %q, want %q", got, "IRON ORE")
	}
	for _, u := range []string{"a", "b", "c"} {
		if st := facStats[u]; st.state != facOK || st.cancel != nil {
			t.Fatalf("machine %s parked %q with cancel=%v", u, st.state, st.cancel != nil)
		}
	}
	if a, _ := h.NodeStore("a")["animating"].(bool); a {
		t.Fatal("settle must drop the animating flag")
	}
	if !strings.Contains(h.flash, "delivered") {
		t.Fatalf("done flash = %q", h.flash)
	}
}

// TestFacLineFailsOnExit: a machine's nonzero exit fails the line — its ✗ chip
// carries the exit, downstream machines reset to idle.
func TestFacLineFailsOnExit(t *testing.T) {
	h := newFakeHost(t)
	kids := facTestLine(
		[2]string{database.TypeMiner, "echo boom >&2; exit 3"},
		[2]string{database.TypeChest, ""},
	)
	facPump(t, h, facRun(h, kids[0]))
	st := facStats["a"]
	if st.state != facError || !strings.Contains(st.note, "exit 3") || !strings.Contains(st.note, "boom") {
		t.Fatalf("miner chip = %q %q", st.state, st.note)
	}
	if facStats["b"].state != "" {
		t.Fatal("downstream chest must reset to idle after a failure")
	}
	if !strings.Contains(h.flash, "failed") {
		t.Fatalf("flash = %q", h.flash)
	}
}

// TestFacRerunWhileRunningStops: alt+r on a running line cancels it instead of
// double-running; the pending wait settles as "line stopped".
func TestFacRerunWhileRunningStops(t *testing.T) {
	h := newFakeHost(t)
	kids := facTestLine([2]string{database.TypeMiner, "sleep 30"})
	cmd := facRun(h, kids[0])
	if stop := facRun(h, kids[0]); stop != nil {
		t.Fatal("alt+r on a running line must stop, not relaunch")
	}
	facPump(t, h, cmd)
	if st := facStats["a"]; st.state == facRunning || st.state == facQueued || st.cancel != nil {
		t.Fatalf("stopped machine parked %q", st.state)
	}
	if h.flash != "line stopped" {
		t.Fatalf("flash = %q", h.flash)
	}
}

// TestFacPrefixChips: each state wears its chip — idle none, running the
// yellow-on-blue belt, ok the ⟨…⟩ payload chip, blocked ⊘, error ✗.
func TestFacPrefixChips(t *testing.T) {
	facStats = map[string]*facStat{}
	if facPrefix("nope") != "" {
		t.Fatal("idle machines wear no chip")
	}
	cases := []struct{ state, note, want string }{
		{facQueued, "", "▸▸▸"},
		{facRunning, "", "▸"}, // color codes sit between the belt's arrows
		{facOK, "1.2k", "⟨"},
		{facBlocked, "blocked", "⊘"},
		{facError, "exit 1", "✗"},
	}
	for _, c := range cases {
		facStats["m"] = &facStat{state: c.state, note: c.note}
		got := facPrefix("m")
		if !strings.Contains(got, c.want) {
			t.Fatalf("%s chip = %q, want %q in it", c.state, got, c.want)
		}
		if !strings.Contains(got, "\x1b[38;2;") {
			t.Fatalf("%s chip must be colored: %q", c.state, got)
		}
	}
}

// TestFacViewPayload: alt+e declines while nothing has flowed, opens on a held
// payload, and its bands carry the payload lines.
func TestFacViewPayload(t *testing.T) {
	h := newFakeHost(t)
	kids := facTestLine([2]string{database.TypeChest, "box"})
	n := kids[0]
	if (facView{}).Enter(h, n) {
		t.Fatal("view must decline with no payload")
	}
	facStats["a"] = &facStat{state: facOK, note: "2L · 8b", payload: "iron\ngear"}
	if !(facView{}).Enter(h, n) {
		t.Fatal("view must open on a held payload")
	}
	if got := (facView{}).Lines(h, n, 80); got != 3 {
		t.Fatalf("Lines = %d, want header + 2 payload rows", got)
	}
	bands := (facView{}).Bands(h, n, "", 80, 0, 10, true)
	joined := strings.Join(bands, "\n")
	if !strings.Contains(joined, "iron") || !strings.Contains(joined, "gear") {
		t.Fatalf("bands must carry the payload: %q", joined)
	}
	// scrolling keys stay inside the view
	if _, handled := (facView{}).Key(h, n, tea.KeyMsg{Type: tea.KeyDown}); !handled {
		t.Fatal("↓ must scroll the view")
	}
}
