package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

// setGenUITestDir points the genui registry at a temp dir and restores (and
// clears) the package state when the test ends.
func setGenUITestDir(t *testing.T) string {
	t.Helper()
	old := genUIDir
	genUIDir = t.TempDir()
	t.Cleanup(func() {
		genUIDir = old
		genUITypes, genUIByKey, loadedGenUI = nil, map[string]nodeType{}, nil
	})
	return genUIDir
}

// writeGenUIFile drops one node-type file into the test dir.
func writeGenUIFile(t *testing.T, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(genUIDir, name), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenUILogType(t *testing.T) {
	setGenUITestDir(t)
	writeGenUIFile(t, "log.js", database.SeedLogArtifactSource)
	loadGenUINodes()

	nt := typeOf(database.TypeLog)
	if nt.label != "Log" {
		t.Fatalf("log type not registered: got label %q", nt.label)
	}
	if !nt.inlineEditable {
		t.Fatal("log type must stay inline-editable")
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

func TestGenUIRunAndChip(t *testing.T) {
	setGenUITestDir(t)
	t.Cleanup(func() { delete(chipKinds, "stamp") })

	writeGenUIFile(t, "dice.js", `
lflow.registerType({
    key: "dice", label: "Dice", sign: "⚂ ", inlineEditable: true,
    run: function (node) { return "echo rolled"; },
});
lflow.registerChip({
    key: "stamp", marker: "◷", color: "cyan",
    display: function (v) { return "◷ " + v; },
});`)
	loadGenUINodes()

	nt := typeOf("dice")
	if nt.label != "Dice" || nt.sign != "⚂ " || nt.run == nil || nt.view == nil {
		t.Fatalf("dice type incomplete: %+v", nt)
	}

	ck, ok := chipKindOf("stamp")
	if !ok {
		t.Fatal("stamp chip kind not registered")
	}
	if got := ck.display("12:00"); got != "◷ 12:00" {
		t.Fatalf("chip display = %q", got)
	}

	// a broken file is listed with its error and falls back to bullets
	writeGenUIFile(t, "broken.js", "syntax error(")
	loadGenUINodes()
	if typeOf("broken").key != database.TypeBullets {
		t.Fatal("broken type must fall back to bullets")
	}
	var found bool
	for _, gn := range loadedGenUI {
		if gn.Key == "broken" && gn.loadErr != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("broken type's load error must be recorded")
	}
}

// TestGenUIMigrationExportsLegacyRows drives the one-time artifacts-table →
// files export: rows become <key>.js (or .js.disabled), and a DB with nothing
// to export seeds the reference log.js instead.
func TestGenUIMigrationExportsLegacyRows(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	old := genUIDir
	t.Cleanup(func() {
		genUIDir = old
		genUITypes, genUIByKey, loadedGenUI = nil, map[string]nodeType{}, nil
	})

	if _, err := db.Exec(`INSERT INTO artifacts (key, label, version, source, created_by, created_at, enabled)
		VALUES ('dice', 'Dice', 1, 'lflow.registerType({key: "dice", label: "Dice"});', 'Pi', 1, true),
		       ('off',  'Off',  1, 'lflow.registerType({key: "off", label: "Off"});',   'Pi', 2, false)`); err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	initGenUINodes(cfg, db)

	if _, err := os.Stat(filepath.Join(cfg, "lflow", "nodes", "dice.js")); err != nil {
		t.Fatal("enabled row must export as dice.js")
	}
	if _, err := os.Stat(filepath.Join(cfg, "lflow", "nodes", "off.js.disabled")); err != nil {
		t.Fatal("disabled row must export as off.js.disabled")
	}
	if typeOf("dice").label != "Dice" {
		t.Fatal("exported type must load into the registry")
	}

	// the export runs once: a second init must not resurrect deleted files
	deleteGenUINode("dice")
	initGenUINodes(cfg, db)
	if _, ok := genUIRowFor("dice"); ok {
		t.Fatal("re-init must not re-export deleted types")
	}

	// a fresh install (no legacy rows) seeds the reference log.js
	fresh := database.InitTestMemoryDB(t)
	cfg2 := t.TempDir()
	initGenUINodes(cfg2, fresh)
	if typeOf(database.TypeLog).label != "Log" {
		t.Fatal("fresh install must seed log.js")
	}
}

// TestTypePickerManagesGenUINodes drives the merged management surface: space
// on a genui row renames the file to .disabled, a disabled type still lists
// (muted) and re-enables on Enter, ctrl+d deletes the file.
func TestTypePickerManagesGenUINodes(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	dir := setGenUITestDir(t)
	writeGenUIFile(t, "dice.js", `lflow.registerType({key: "dice", label: "Dice", inlineEditable: true});`)
	loadGenUINodes()

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
		t.Fatal("enabled genui node must list in /type")
	}

	// space disables it in place — the file gains the .disabled suffix
	m.list.sel = sel
	p := &m.list
	if !src.onKey(m, p, " ", items) {
		t.Fatal("space on a genui row must be handled")
	}
	if gn, _ := genUIRowFor("dice"); gn.Enabled {
		t.Fatal("space must disable the type")
	}
	if _, err := os.Stat(filepath.Join(dir, "dice.js.disabled")); err != nil {
		t.Fatal("disabling must rename the file to .disabled")
	}
	// disabled types still list (muted) so they can come back
	items = src.items(m, "")
	found := false
	for _, it := range items {
		if it.value == "dice" {
			found = true
		}
	}
	if !found {
		t.Fatal("disabled genui node must still list in /type")
	}
	// Enter on the disabled row re-enables and applies the type
	m.cursor = 0
	src.onSelect(m, pickerItem{value: "dice"})
	if gn, _ := genUIRowFor("dice"); !gn.Enabled {
		t.Fatal("selecting a disabled type must re-enable it")
	}
	if n.typ != "dice" {
		t.Fatalf("node type = %q, want dice", n.typ)
	}

	// ctrl+d uninstalls — the file is gone
	items = src.items(m, "")
	for i, it := range items {
		if it.value == "dice" {
			p.sel = i
		}
	}
	if !src.onKey(m, p, "ctrl+d", items) {
		t.Fatal("ctrl+d on a genui row must be handled")
	}
	if _, ok := genUIRowFor("dice"); ok {
		t.Fatal("ctrl+d must uninstall the type")
	}
	if _, err := os.Stat(filepath.Join(dir, "dice.js")); !os.IsNotExist(err) {
		t.Fatal("ctrl+d must delete the file")
	}
}
