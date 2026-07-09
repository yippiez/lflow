package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

// testLogMod is a log-shaped fixture mod: the same shape as the external log
// mod (github.com/yippiez/lflow-log). It exercises every descriptor hook the
// nodemod bridge exposes — sign, glyph, baseColor, prefix, muteFrom.
const testLogMod = `lflow.registerType({
    key: "log",
    label: "Log",
    inlineEditable: true,
    sign: "-> ",
    glyph: function (node) { return ["→", node.color || "dim"]; },
    baseColor: function (node) { return node.color || "dim"; },
    prefix: function (node) {
        return lflow.style("(" + lflow.time(node.addedOn) + ") ", "dim");
    },
    muteFrom: function (name) { return name.indexOf(" · "); },
});
`

// setModTestDir points the mod registry at a temp dir and restores (and
// clears) the package state when the test ends.
func setModTestDir(t *testing.T) string {
	t.Helper()
	old := modsDir
	modsDir = t.TempDir()
	t.Cleanup(func() {
		modsDir = old
		modTypes, modByKey, loadedMods = nil, map[string]nodeType{}, nil
	})
	return modsDir
}

// writeModFile drops one node-type file into the test dir.
func writeModFile(t *testing.T, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(modsDir, name), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNodeModLogType(t *testing.T) {
	setModTestDir(t)
	writeModFile(t, "log.js", testLogMod)
	loadNodeMods()

	nt := typeOf("log")
	if nt.label != "Log" {
		t.Fatalf("log type not registered: got label %q", nt.label)
	}
	if !nt.inlineEditable {
		t.Fatal("log type must stay inline-editable")
	}

	// a mod-declared sign drives convertBySign: typing "->" then the trigger
	// space (consumed, not stored) converts and strips the sign.
	it := &item{uuid: "c", typ: "bullets", name: "->deployed"}
	tr := &tree{byUUID: map[string]*item{"c": it}}
	m := &Model{tree: tr, caret: 2}
	if !m.convertBySign(it) || it.typ != "log" || it.name != "deployed" {
		t.Fatalf("mod sign '-> ' must convert to log: typ=%q name=%q", it.typ, it.name)
	}

	it = &item{typ: "log", name: "deploy · went fine", addedOn: time.Date(2026, 7, 1, 14, 30, 0, 0, time.Local).UnixNano()}

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
	if order[len(order)-1] != "log" {
		t.Fatalf("typeOrder tail = %v, want log last", order[len(order)-3:])
	}
}

func TestNodeModRunAndChip(t *testing.T) {
	setModTestDir(t)
	t.Cleanup(func() { delete(chipKinds, "stamp") })

	writeModFile(t, "dice.js", `
lflow.registerType({
    key: "dice", label: "Dice", sign: "⚂ ", inlineEditable: true,
    run: function (node) { return "echo rolled"; },
});
lflow.registerChip({
    key: "stamp", marker: "◷", color: "cyan",
    display: function (v) { return "◷ " + v; },
});`)
	loadNodeMods()

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
	writeModFile(t, "broken.js", "syntax error(")
	loadNodeMods()
	if typeOf("broken").key != database.TypeBullets {
		t.Fatal("broken type must fall back to bullets")
	}
	var found bool
	for _, gn := range loadedMods {
		if gn.Key == "broken" && gn.loadErr != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("broken type's load error must be recorded")
	}
}

// TestNodeModMigrationExportsLegacyRows drives the one-time artifacts-table →
// files export: rows become <key>.js (or .js.disabled), and a DB with nothing
// to export leaves the mods dir empty.
func TestNodeModMigrationExportsLegacyRows(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	old := modsDir
	t.Cleanup(func() {
		modsDir = old
		modTypes, modByKey, loadedMods = nil, map[string]nodeType{}, nil
	})

	if _, err := db.Exec(`INSERT INTO artifacts (key, label, version, source, created_by, created_at, enabled)
		VALUES ('dice', 'Dice', 1, 'lflow.registerType({key: "dice", label: "Dice"});', 'Pi', 1, true),
		       ('off',  'Off',  1, 'lflow.registerType({key: "off", label: "Off"});',   'Pi', 2, false)`); err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	initNodeMods(cfg, db)

	if _, err := os.Stat(filepath.Join(cfg, "lflow", "mods", "dice.js")); err != nil {
		t.Fatal("enabled row must export as dice.js")
	}
	if _, err := os.Stat(filepath.Join(cfg, "lflow", "mods", "off.js.disabled")); err != nil {
		t.Fatal("disabled row must export as off.js.disabled")
	}
	if typeOf("dice").label != "Dice" {
		t.Fatal("exported type must load into the registry")
	}

	// the export runs once: a second init must not resurrect deleted files
	deleteNodeMod("dice")
	initNodeMods(cfg, db)
	if _, ok := modRowFor("dice"); ok {
		t.Fatal("re-init must not re-export deleted types")
	}

	// a fresh install (no legacy rows) leaves the mods dir empty — no built-ins
	fresh := database.InitTestMemoryDB(t)
	cfg2 := t.TempDir()
	initNodeMods(cfg2, fresh)
	if len(loadedMods) != 0 {
		t.Fatalf("fresh install must seed nothing, got %d mods", len(loadedMods))
	}
}

// TestNodeModDirMigrationAndDirMods: the pre-rename nodes/ dir moves to mods/
// wholesale, and a git-installed <key>/ directory (mod.json + entry) loads,
// disables by directory rename, and deletes recursively.
func TestNodeModDirMigrationAndDirMods(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	old := modsDir
	t.Cleanup(func() {
		modsDir = old
		modTypes, modByKey, loadedMods = nil, map[string]nodeType{}, nil
	})

	// the old nodes/ dir migrates to mods/ with its files intact
	cfg := t.TempDir()
	oldDir := filepath.Join(cfg, "lflow", "nodes")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "log.js"), []byte(testLogMod), 0o644); err != nil {
		t.Fatal(err)
	}
	initNodeMods(cfg, db)
	if typeOf("log").label != "Log" {
		t.Fatal("nodes/ content must survive the rename to mods/")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("the old nodes/ dir must be gone after migration")
	}

	// a directory mod: mod.json names the entry; the manifest name wins
	dice := filepath.Join(modsDir, "lflow-dice")
	if err := os.MkdirAll(dice, 0o755); err != nil {
		t.Fatal(err)
	}
	writeModFile(t, "lflow-dice/mod.json", `{"name":"dice","description":"roll","entry":"dice.js","version":"0.1.0"}`)
	writeModFile(t, "lflow-dice/dice.js", `lflow.registerType({key: "dice", label: "Dice", inlineEditable: true});`)
	loadNodeMods()
	if typeOf("dice").label != "Dice" {
		t.Fatal("directory mod must register via its mod.json entry")
	}

	// disable renames the DIRECTORY; the type falls back to bullets
	setNodeModEnabled("dice", false)
	if _, err := os.Stat(dice + ".disabled"); err != nil {
		t.Fatal("disabling a dir mod must rename the directory")
	}
	if typeOf("dice").key != database.TypeBullets {
		t.Fatal("disabled dir mod must fall back to bullets")
	}
	setNodeModEnabled("dice", true)
	if typeOf("dice").label != "Dice" {
		t.Fatal("re-enabling a dir mod must restore it")
	}

	// delete removes the whole directory
	deleteNodeMod("dice")
	if _, err := os.Stat(dice); !os.IsNotExist(err) {
		t.Fatal("deleting a dir mod must remove the directory")
	}
}

// TestTypePickerManagesNodeMods drives the merged management surface: space
// on a mod row renames the file to .disabled, a disabled type still lists
// (muted) and re-enables on Enter, ctrl+d deletes the file.
func TestTypePickerManagesNodeMods(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	dir := setModTestDir(t)
	writeModFile(t, "dice.js", `lflow.registerType({key: "dice", label: "Dice", inlineEditable: true});`)
	loadNodeMods()

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
		t.Fatal("enabled mod node must list in /type")
	}

	// space disables it in place — the file gains the .disabled suffix
	m.list.sel = sel
	p := &m.list
	if !src.onKey(m, p, " ", items) {
		t.Fatal("space on a mod row must be handled")
	}
	if gn, _ := modRowFor("dice"); gn.Enabled {
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
		t.Fatal("disabled mod node must still list in /type")
	}
	// Enter on the disabled row re-enables and applies the type
	m.cursor = 0
	src.onSelect(m, pickerItem{value: "dice"})
	if gn, _ := modRowFor("dice"); !gn.Enabled {
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
		t.Fatal("ctrl+d on a mod row must be handled")
	}
	if _, ok := modRowFor("dice"); ok {
		t.Fatal("ctrl+d must uninstall the type")
	}
	if _, err := os.Stat(filepath.Join(dir, "dice.js")); !os.IsNotExist(err) {
		t.Fatal("ctrl+d must delete the file")
	}
}
