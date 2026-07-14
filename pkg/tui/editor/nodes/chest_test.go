package nodes

import (
	"context"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestChestStage: a chest holds the arriving payload and passes it on
// unchanged; an empty belt leaves an "empty" chip.
func TestChestStage(t *testing.T) {
	o := chestStage(context.Background(), "ignored label", "iron\ngear")
	if o.payload != "iron\ngear" || o.note != "2L · 9b" {
		t.Fatalf("chest = %+v", o)
	}
	if o := chestStage(context.Background(), "", ""); o.note != "empty" {
		t.Fatalf("empty chest = %+v", o)
	}
}

// TestChestContext: an agent sees what the chest holds — label line then
// payload — and a bare <chest> element when nothing has flowed.
func TestChestContext(t *testing.T) {
	h := newFakeHost(t)
	kids := facTestLine([2]string{database.TypeChest, "box"})
	if _, _, body := chestContext(h, kids[0]); body != "" {
		t.Fatalf("empty chest context body = %q", body)
	}
	facStats["a"] = &facStat{state: facOK, payload: "iron plate"}
	tag, _, body := chestContext(h, kids[0])
	if tag != "chest" || body != "box\niron plate" {
		t.Fatalf("chest context = %q %q", tag, body)
	}
}
