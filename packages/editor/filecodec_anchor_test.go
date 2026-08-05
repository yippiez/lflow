package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// TestRenderFromDBExpandsChipAnchors: a node name minted by a file session
// (typing "#tag " or pasting a URL mints a chip anchor) must never reach the
// saved file as a raw U+FFFC sentinel — RenderFromDB expands it to the
// chip's full value, same as the editor's own resolve.
func TestRenderFromDBExpandsChipAnchors(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	root := database.Node{UUID: "docroot", Name: "doc", Type: database.TypeBullets}
	if err := root.Insert(db); err != nil {
		t.Fatal(err)
	}
	chip := database.Chip{ID: "chip1", Kind: "tag", Value: "#urgent"}
	if err := database.UpsertChip(db, chip); err != nil {
		t.Fatal(err)
	}
	name := "todo item " + database.ChipAnchor("chip1")
	n := database.Node{UUID: "n1", ParentUUID: "docroot", Name: name, Type: database.TypeBullets}
	if err := n.Insert(db); err != nil {
		t.Fatal(err)
	}

	out, err := RenderFromDB(db, "docroot", markdownCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out, database.ChipSentinel) {
		t.Fatalf("rendered output still carries a chip sentinel: %q", out)
	}
	if !strings.Contains(out, chip.Value) {
		t.Fatalf("rendered output missing expanded chip value %q: %q", chip.Value, out)
	}
}
