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

// TestTypePickerManagesArtifacts drives the merged management surface: space
// on an artifact row toggles it, a disabled artifact still lists (muted) and
// re-enables on Enter, ctrl+d uninstalls — /artifacts is gone.
func TestTypePickerManagesArtifacts(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	defer func() { artifactTypes, artifactByKey, loadedArtifacts = nil, map[string]nodeType{}, nil }()
	a := database.Artifact{Key: "dice", Label: "Dice", Version: 1, Source: artifactSourceDice(t), CreatedBy: "user", Enabled: true}
	if err := installArtifact(db, a); err != nil {
		t.Fatal(err)
	}

	root := &item{uuid: "root"}
	n := &item{uuid: "n1", name: "roll", parent: root}
	root.children = []*item{n}
	tr := &tree{db: db, root: root, byUUID: map[string]*item{"root": root, "n1": n},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30}
	m.refreshRows()

	src := typeSource{}
	items := src.items(m, "")
	sel := -1
	for i, it := range items {
		if it.value == "dice" {
			sel = i
		}
	}
	if sel < 0 {
		t.Fatal("enabled artifact must list in /type")
	}

	// space disables it in place
	m.list.sel = sel
	p := &m.list
	if !src.onKey(m, p, " ", items) {
		t.Fatal("space on an artifact row must be handled")
	}
	if la, _ := artifactRowFor("dice"); la.Enabled {
		t.Fatal("space must disable the artifact")
	}
	// disabled artifacts still list (muted) so they can come back
	items = src.items(m, "")
	found := false
	for _, it := range items {
		if it.value == "dice" {
			found = true
		}
	}
	if !found {
		t.Fatal("disabled artifact must still list in /type")
	}
	// Enter on the disabled row re-enables and applies the type
	m.cursor = 0
	src.onSelect(m, pickerItem{value: "dice"})
	if la, _ := artifactRowFor("dice"); !la.Enabled {
		t.Fatal("selecting a disabled artifact must re-enable it")
	}
	if n.typ != "dice" {
		t.Fatalf("node type = %q, want dice", n.typ)
	}

	// ctrl+d uninstalls
	items = src.items(m, "")
	for i, it := range items {
		if it.value == "dice" {
			p.sel = i
		}
	}
	if !src.onKey(m, p, "ctrl+d", items) {
		t.Fatal("ctrl+d on an artifact row must be handled")
	}
	if _, ok := artifactRowFor("dice"); ok {
		t.Fatal("ctrl+d must uninstall the artifact")
	}
	if _, err := database.GetArtifact(db, "dice"); err == nil {
		t.Fatal("uninstall must delete the DB row")
	}
}

// artifactSourceDice returns a minimal valid artifact program for tests.
func artifactSourceDice(t *testing.T) string {
	t.Helper()
	return `lflow.registerType({key: "dice", label: "Dice", inlineEditable: true});`
}
