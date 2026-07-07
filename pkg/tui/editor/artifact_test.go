package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

// seedLogArtifact installs the seeded log artifact into a test DB and loads
// the runtime registry from it.
func seedLogArtifact(t *testing.T, db *database.DB) {
	t.Helper()
	a := database.Artifact{
		Key: "log", Label: "Log", Version: 1,
		Source: database.SeedLogArtifactSource, CreatedBy: "seed",
		CreatedAt: time.Now().UnixNano(), Enabled: true,
	}
	if err := a.Upsert(db); err != nil {
		t.Fatal(err)
	}
	loadArtifacts(db)
}

func TestArtifactLogType(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	defer func() { loadArtifacts(db) }() // registry is package state; leave it empty for other tests
	seedLogArtifact(t, db)

	nt := typeOf(database.TypeLog)
	if nt.label != "Log" {
		t.Fatalf("log artifact not registered: got label %q", nt.label)
	}
	if !nt.inlineEditable {
		t.Fatal("log artifact must stay inline-editable")
	}

	it := &item{typ: database.TypeLog, name: "deploy · went fine", addedOn: time.Date(2026, 7, 1, 14, 30, 0, 0, time.Local).UnixNano()}

	// glyph: → tinted dim by default, by the /color otherwise
	g, col := nt.glyph(it)
	if g != "→" || col != cDim {
		t.Fatalf("glyph = %q %q, want → dim", g, col)
	}
	it.style = "color:red"
	if _, col = nt.glyph(it); col != styleColorCode["red"] {
		t.Fatalf("colored glyph = %q, want red", col)
	}

	// prefix: the muted time chip from the node's creation time
	if p := nt.prefix(it); !strings.Contains(p, "(2026-07-01 14:30)") {
		t.Fatalf("prefix = %q, want the time chip", p)
	}

	// muteFrom: the " · " tail is muted
	if d := nt.muteFrom("deploy · went fine"); d != 6 {
		t.Fatalf("muteFrom = %d, want 6", d)
	}
	if d := nt.muteFrom("no separator"); d != -1 {
		t.Fatalf("muteFrom = %d, want -1", d)
	}

	// the picker lists it after the built-ins, indistinguishable from them
	order := typeOrder()
	if order[len(order)-1] != database.TypeLog {
		t.Fatalf("typeOrder tail = %v, want log last", order[len(order)-3:])
	}
}

func TestArtifactRunAndChip(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	defer func() {
		loadArtifacts(db)
		delete(chipKinds, "stamp")
	}()

	a := database.Artifact{
		Key: "dice", Label: "Dice", Version: 1, CreatedBy: "Pi",
		CreatedAt: time.Now().UnixNano(), Enabled: true,
		Source: `
lflow.registerType({
    key: "dice", label: "Dice", sign: "⚂ ", inlineEditable: true,
    run: function (node) { return "echo rolled"; },
});
lflow.registerChip({
    key: "stamp", marker: "◷", color: "cyan",
    display: function (v) { return "◷ " + v; },
});`,
	}
	if err := a.Upsert(db); err != nil {
		t.Fatal(err)
	}
	loadArtifacts(db)

	nt := typeOf("dice")
	if nt.label != "Dice" || nt.sign != "⚂ " || nt.run == nil || nt.view == nil {
		t.Fatalf("dice artifact incomplete: %+v", nt)
	}

	ck, ok := chipKindOf("stamp")
	if !ok {
		t.Fatal("stamp chip kind not registered")
	}
	if got := ck.display("12:00"); got != "◷ 12:00" {
		t.Fatalf("chip display = %q", got)
	}

	// a broken artifact is listed with its error and falls back to bullets
	bad := database.Artifact{Key: "broken", Label: "Broken", Source: "syntax error(", Enabled: true}
	if err := bad.Upsert(db); err != nil {
		t.Fatal(err)
	}
	loadArtifacts(db)
	if typeOf("broken").key != database.TypeBullets {
		t.Fatal("broken artifact must fall back to bullets")
	}
	var found bool
	for _, la := range loadedArtifacts {
		if la.Key == "broken" && la.loadErr != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("broken artifact's load error must be recorded")
	}
}
