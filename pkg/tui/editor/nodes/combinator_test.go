package nodes

import (
	"context"
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestCombineStage: exit 0 passes the payload untouched, a nonzero exit closes
// the gate (blocked, not an error), an empty combinator is an open gate.
func TestCombineStage(t *testing.T) {
	o := combineStage(context.Background(), `grep -q iron`, "iron plate")
	if o.blocked || o.err != "" || o.payload != "iron plate" {
		t.Fatalf("open gate = %+v", o)
	}
	o = combineStage(context.Background(), `grep -q copper`, "iron plate")
	if !o.blocked || o.err != "" {
		t.Fatalf("closed gate = %+v", o)
	}
	if o := combineStage(context.Background(), "", "x"); o.blocked || o.payload != "x" {
		t.Fatalf("empty combinator = %+v", o)
	}
}

// TestCombinatorBlocksLine: a closed gate halts the belt — the combinator
// wears ⊘, machines beyond it never run, machines before it keep their chips.
func TestCombinatorBlocksLine(t *testing.T) {
	h := newFakeHost(t)
	kids := facTestLine(
		[2]string{database.TypeMiner, "printf 'copper ore'"},
		[2]string{database.TypeCombinator, "grep -q iron"},
		[2]string{database.TypeChest, "box"},
	)
	facPump(t, h, facRun(h, kids[0]))
	if facStats["a"].state != facOK {
		t.Fatalf("miner chip = %q", facStats["a"].state)
	}
	if facStats["b"].state != facBlocked {
		t.Fatalf("combinator chip = %q", facStats["b"].state)
	}
	if facStats["c"].state != "" || facStats["c"].payload != "" {
		t.Fatal("the chest behind a closed gate must stay idle and empty")
	}
	if !strings.Contains(h.flash, "blocked") {
		t.Fatalf("flash = %q", h.flash)
	}
}
