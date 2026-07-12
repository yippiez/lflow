package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestPlacementHonorsPriorityUp: generic placement follows the asked node's
// priority — replies land on top of a priority-up node, and a "below" reply
// posts above the asked node when the surrounding parent is priority up.
// (Mention nodes themselves are forced down — see TestAgentChipForcesDown —
// so in practice this covers user nodes inside a thread that are set up.)
func TestPlacementHonorsPriorityUp(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	n1.priority = database.PriorityUp

	m.placeAgentNode("n1", "n1", "first reply", "thread")
	m.placeAgentNode("n1", "n1", "second reply", "thread")

	if len(n1.children) != 2 {
		t.Fatalf("want two replies, got %d", len(n1.children))
	}
	if n1.children[0].name != "second reply" || n1.children[1].name != "first reply" {
		t.Fatalf("priority up must stack newest first, got %q, %q",
			n1.children[0].name, n1.children[1].name)
	}
	// agent replies are born down: their own sub-threads read chronologically
	for _, c := range n1.children {
		if c.priority != database.PriorityDown {
			t.Fatalf("reply priority = %q, want down", c.priority)
		}
	}

	// a follow-up under the priority-up node: the "below" reply posts above it
	follow := addChild(m, n1, "f1", "and how do i cap retries?", database.TypeBullets)
	m.placeAgentNode("n1", "f1", "cap at three", "below")
	fi := indexOf(follow)
	if fi <= 0 || n1.children[fi-1].name != "cap at three" {
		t.Fatal("below-reply must land above the asked node under a priority-up parent")
	}
}

// TestAgentChipForcesDown: an agent-chipped node always reads top-down —
// sendThread converts a pre-set up to down, and /priority:up refuses it.
func TestAgentChipForcesDown(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	n := database.Node{UUID: "n1", ParentUUID: "disc", Name: n1.name, Type: database.TypeBullets, Priority: database.PriorityUp}
	if err := n.Insert(m.db); err != nil {
		t.Fatal(err)
	}
	n1.priority = database.PriorityUp

	drain(t, m, startThread(t, m, n1))
	if n1.priority != database.PriorityDown {
		t.Fatalf("sendThread must force the mention down, got %q", n1.priority)
	}
	got, err := database.GetNode(m.db, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != database.PriorityDown {
		t.Fatalf("persisted priority = %q, want down", got.Priority)
	}

	// /priority:up on the mention refuses — the thread stays chronological
	m.refreshRows()
	m.cursor = m.rowIndexOf(n1)
	m.runSlash("/priority:up")
	if n1.priority != database.PriorityDown {
		t.Fatalf("/priority:up must refuse an agent-chipped node, got %q", n1.priority)
	}
}

// TestPriorityDownReplyAppends: down (and unset — every pre-feature node) keeps
// the old order, replies at the bottom.
func TestPriorityDownReplyAppends(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)

	m.placeAgentNode("n1", "n1", "first reply", "thread")
	m.placeAgentNode("n1", "n1", "second reply", "thread")

	if n1.children[0].name != "first reply" || n1.children[1].name != "second reply" {
		t.Fatalf("priority down must append, got %q, %q",
			n1.children[0].name, n1.children[1].name)
	}
}

// TestBuildThreadInvertsPriorityUp: a priority-up node shows newest children on
// top, so the agent context walk inverts them — the agent always reads the
// conversation chronologically, oldest first.
func TestBuildThreadInvertsPriorityUp(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	n1.priority = database.PriorityUp

	// screen order under a priority-up mention: newest first
	for _, name := range []string{"third", "second", "first"} {
		addChild(m, n1, name, name, database.TypeBullets)
	}

	thread := m.buildThread(n1, "n1")
	var kids []string
	for _, tn := range thread {
		if tn.Depth == 2 { // parent=0, mention=1, children=2
			kids = append(kids, tn.Name)
		}
	}
	want := []string{"first", "second", "third"}
	if len(kids) != len(want) {
		t.Fatalf("want %d children in context, got %d", len(want), len(kids))
	}
	for i := range want {
		if kids[i] != want[i] {
			t.Fatalf("context order = %v, want chronological %v", kids, want)
		}
	}

	// down keeps outline order as-is
	n1.priority = database.PriorityDown
	thread = m.buildThread(n1, "n1")
	kids = kids[:0]
	for _, tn := range thread {
		if tn.Depth == 2 {
			kids = append(kids, tn.Name)
		}
	}
	if kids[0] != "third" || kids[2] != "first" {
		t.Fatalf("priority down must keep outline order, got %v", kids)
	}
}

// TestReparentHonorsPriority: /move:to inside the loaded tree lands where the
// target's priority points.
func TestReparentHonorsPriority(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	a := addChild(m, disc, "a1", "existing", database.TypeBullets)

	n1.priority = database.PriorityUp
	if !m.tree.reparent(a, n1) {
		t.Fatal("reparent into the mention failed")
	}
	if n1.children[0] != a {
		t.Fatal("priority up must land the moved node on top")
	}

	b := addChild(m, disc, "b1", "another", database.TypeBullets)
	n1.priority = database.PriorityDown
	if !m.tree.reparent(b, n1) {
		t.Fatal("second reparent failed")
	}
	if n1.children[len(n1.children)-1] != b {
		t.Fatal("priority down must land the moved node at the bottom")
	}
}

// TestPrioritySlashCommand: /priority:up and /priority:down set the node (a
// mirror sets its original) and persist immediately, like /star.
func TestPrioritySlashCommand(t *testing.T) {
	m, disc, _ := newAgentTestModel(t)
	// persist disc so the immediate SetPriority write has a row to hit; disc is
	// a plain node — the mention node refuses up (TestAgentChipForcesDown)
	n := database.Node{UUID: "disc", Name: disc.name, Type: database.TypeBullets}
	if err := n.Insert(m.db); err != nil {
		t.Fatal(err)
	}
	m.cursor = m.rowIndexOf(disc)

	m.runSlash("/priority:up")
	if disc.priority != database.PriorityUp {
		t.Fatalf("priority = %q, want up", disc.priority)
	}
	got, err := database.GetNode(m.db, "disc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != database.PriorityUp {
		t.Fatalf("persisted priority = %q, want up", got.Priority)
	}

	m.runSlash("/priority:down")
	if disc.priority != database.PriorityDown {
		t.Fatalf("priority = %q, want down", disc.priority)
	}
	got, _ = database.GetNode(m.db, "disc")
	if got.Priority != database.PriorityDown {
		t.Fatalf("persisted priority = %q, want down", got.Priority)
	}
}
