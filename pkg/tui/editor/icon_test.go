package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func TestIconCatalogShortcodesUnique(t *testing.T) {
	seen := map[string]string{}
	for _, e := range iconCatalog {
		if e.shortcode == "" || e.glyph == "" {
			t.Fatalf("empty entry: %+v", e)
		}
		if prev, ok := seen[e.shortcode]; ok {
			t.Fatalf("duplicate shortcode %q for %q and %q", e.shortcode, prev, e.glyph)
		}
		seen[e.shortcode] = e.glyph
	}
	// sanity: the agreed set is present
	for _, code := range []string{
		"larrow", "rarrow", "doublearrow", "ldarrow", "rdarrow", "iff", "loop", "rlooparrow",
		"chain", "magnifier", "decision",
		"trello", "cpapers", "zotero", "claude", "obsidian",
		"melt", "shush", "cold", "hand", "no", "warning",
	} {
		if _, ok := seen[code]; !ok {
			t.Errorf("missing shortcode %q", code)
		}
	}
	if _, ok := seen["drive"]; ok {
		t.Error("drive was removed from the catalog")
	}
	if _, ok := seen["sheets"]; ok {
		t.Error("sheets was removed from the catalog")
	}
}

func TestFilterIcons(t *testing.T) {
	all := filterIcons("")
	if len(all) != len(iconCatalog) {
		t.Fatalf("empty filter = %d, want full catalog %d", len(all), len(iconCatalog))
	}
	got := filterIcons("rarr")
	if len(got) != 1 || got[0].shortcode != "rarrow" {
		t.Fatalf("filter rarr = %v, want [rarrow]", got)
	}
	// leading colon on the query is ignored
	got = filterIcons(":loop")
	if len(got) < 1 {
		t.Fatal("filter :loop returned nothing")
	}
	for _, e := range got {
		if !strings.Contains(e.shortcode, "loop") {
			t.Fatalf("filter :loop included %q", e.shortcode)
		}
	}
	if got = filterIcons("nope-not-an-icon"); len(got) != 0 {
		t.Fatalf("unknown filter = %v, want empty", got)
	}
}

func TestColonOpensIconPicker(t *testing.T) {
	m := newTestModel(80, "")
	m.cursor = 0
	m.caret = 0
	m.press(":")
	if m.mode != modeComplete || m.compl.kind != complIcon {
		t.Fatalf("mode/kind = %v/%v, want modeComplete/complIcon", m.mode, m.compl.kind)
	}
	cur := m.cursorItem()
	if cur == nil || cur.name != ":" {
		t.Fatalf("name = %q, want \":\"", curName(cur))
	}
	items := m.complItems("")
	if len(items) != len(iconCatalog) {
		t.Fatalf("icon items = %d, want %d", len(items), len(iconCatalog))
	}
}

func TestApplyCompletionIconInsertsGlyph(t *testing.T) {
	it := &item{uuid: "n", typ: database.TypeBullets, name: "hi :rar"}
	m := &Model{
		tree:  &tree{byUUID: map[string]*item{"n": it}},
		caret: len([]rune("hi :rar")),
		compl: complState{kind: complIcon, start: len([]rune("hi "))},
	}
	e, ok := iconByShortcode("rarrow")
	if !ok {
		t.Fatal("rarrow missing")
	}
	m.applyCompletion(it, pickerItem{value: e.glyph, label: ":" + e.shortcode})
	if it.name != "hi →" {
		t.Fatalf("name = %q, want %q", it.name, "hi →")
	}
	if m.caret != len([]rune("hi →")) {
		t.Errorf("caret = %d, want end of glyph", m.caret)
	}
}

// TestApplyCompletionPaintedIconInsertsChip: brand-colored icons land as icon
// chips (value=glyph, label=shortcode) so render can keep the color — plain
// unicode would leave a bare "Z" that can't be told from prose.
func TestApplyCompletionPaintedIconInsertsChip(t *testing.T) {
	it := &item{uuid: "n", typ: database.TypeBullets, name: ":claude"}
	m := &Model{
		tree:  &tree{byUUID: map[string]*item{"n": it}},
		caret: len([]rune(":claude")),
		compl: complState{kind: complIcon, start: 0},
		chips: map[string]database.Chip{},
	}
	e, ok := iconByShortcode("claude")
	if !ok {
		t.Fatal("claude missing")
	}
	m.applyCompletion(it, pickerItem{value: e.glyph, label: ":" + e.shortcode})
	if !hasAnchor(it.name) {
		t.Fatalf("painted icon must be a chip anchor, name=%q", it.name)
	}
	var c database.Chip
	for _, ch := range m.chips {
		c = ch
		break
	}
	if c.Kind != chipKindIcon || c.Value != e.glyph || c.Label != e.shortcode {
		t.Fatalf("chip = %+v, want kind=icon value=%q label=%q", c, e.glyph, e.shortcode)
	}
	// render paints the brand color (red for claude)
	body := renderBody(it, it.name, -1, false, m.chips, false)
	if !strings.Contains(body, e.glyph) {
		t.Fatalf("render missing glyph: %q", body)
	}
	if !strings.Contains(body, cRed) && !strings.Contains(body, styleColorCode["red"]) {
		t.Fatalf("render missing red paint for claude: %q", body)
	}
	// white icons stay plain — no chip
	it2 := &item{uuid: "n2", typ: database.TypeBullets, name: ":rarrow"}
	m.compl = complState{kind: complIcon, start: 0}
	m.caret = len([]rune(":rarrow"))
	r, _ := iconByShortcode("rarrow")
	m.applyCompletion(it2, pickerItem{value: r.glyph, label: ":" + r.shortcode})
	if hasAnchor(it2.name) || it2.name != r.glyph {
		t.Fatalf("white icon should be plain glyph, name=%q", it2.name)
	}
}

func TestIconPickViaKeys(t *testing.T) {
	m := newTestModel(80, "")
	m.cursor = 0
	m.caret = 0
	m.press(":")
	m.press("r")
	m.press("a")
	m.press("r")
	// filter should leave rarrow (and maybe others containing "rar")
	items := completerSource{}.items(m, m.list.query)
	if len(items) == 0 {
		t.Fatal("no icon matches for rar")
	}
	// pick the highlighted row
	m.press("enter")
	if m.mode != modeOutline {
		t.Fatalf("mode = %v after pick, want outline", m.mode)
	}
	cur := m.cursorItem()
	if cur == nil || !strings.Contains(cur.name, "→") {
		t.Fatalf("name = %q, want a rarrow glyph", curName(cur))
	}
	if strings.Contains(cur.name, ":") {
		t.Fatalf("shortcode leaked into name: %q", cur.name)
	}
}

func TestQueryColonStaysQueryCmd(t *testing.T) {
	root := &item{}
	it := &item{uuid: "q", typ: database.TypeQuery, name: "", parent: root}
	root.children = []*item{it}
	m := &Model{
		tree:      &tree{root: root, byUUID: map[string]*item{"q": it}, externalNames: map[string]string{}},
		viewStack: []*item{root},
		width:     80,
		height:    24,
	}
	m.refreshRows()
	m.cursor = 0
	m.caret = 0
	m.press(":")
	if m.compl.kind != complQueryCmd {
		t.Fatalf("query node \":\" opened kind %v, want complQueryCmd", m.compl.kind)
	}
}

func TestInsertIconOnQueryNode(t *testing.T) {
	root := &item{}
	it := &item{uuid: "q", typ: database.TypeQuery, name: "find ", parent: root}
	root.children = []*item{it}
	m := &Model{
		tree:      &tree{root: root, byUUID: map[string]*item{"q": it}, externalNames: map[string]string{}},
		viewStack: []*item{root},
		width:     80,
		height:    24,
		caret:     len([]rune("find ")),
	}
	m.refreshRows()
	m.cursor = 0
	mm, _ := m.insertChip("icon")
	m = mm.(*Model)
	if m.mode != modeComplete || m.compl.kind != complIcon {
		t.Fatalf("insert icon → mode/kind = %v/%v, want complete/icon", m.mode, m.compl.kind)
	}
	// chips stay disabled on query; icon path must not flash the chip error
	if strings.Contains(m.flash, "chips are disabled") {
		t.Fatalf("icon insert hit chips guard: %q", m.flash)
	}
	cur := m.cursorItem()
	if cur == nil || !strings.HasSuffix(cur.name, ":") {
		t.Fatalf("name = %q, want trailing \":\" trigger", curName(cur))
	}
}

func curName(it *item) string {
	if it == nil {
		return "<nil>"
	}
	return it.name
}
