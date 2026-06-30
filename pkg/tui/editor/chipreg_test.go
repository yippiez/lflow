package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// menuModel builds a Model whose cursor sits on a node of the given type, with an
// open "@" chip menu carrying query q.
func menuModel(typ, q string) (*Model, *item) {
	it := &item{uuid: "n", typ: typ, name: "note "}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{
		tree:   tr,
		rows:   []row{{it: it}},
		cursor: 0,
		caret:  len([]rune("note ")),
	}
	m.compl = complState{kind: complChipMenu, start: m.caret, query: q}
	return m, it
}

func TestChipMenuItemsBulletsListsAll(t *testing.T) {
	m, _ := menuModel(database.TypeBullets, "")
	items := m.complItems()
	if len(items) != len(chipSpecs) {
		t.Fatalf("@ menu on bullets = %d items, want %d (every kind)", len(items), len(chipSpecs))
	}
}

func TestChipMenuItemsFilter(t *testing.T) {
	m, _ := menuModel(database.TypeBullets, "fi")
	items := m.complItems()
	if len(items) != 1 || items[0].value != chipKindPath {
		t.Fatalf("@ menu filter 'fi' = %v, want [path]", items)
	}
}

func TestChipMenuItemsBashEmpty(t *testing.T) {
	m, _ := menuModel(database.TypeBash, "")
	if items := m.complItems(); len(items) != 0 {
		t.Fatalf("@ menu on bash = %v, want none (every kind gated out)", items)
	}
	if anyChipAllowed(database.TypeBash) {
		t.Fatal("anyChipAllowed(bash) = true, want false")
	}
}

func TestChipMenuItemsCodeOnlyCmd(t *testing.T) {
	// code keeps ">"/"[["/"#" literal (file/link/tag/date gated out), but a cmd chip
	// works on code just as the "$cmd  " fast path always has — so @ offers only cmd.
	m, _ := menuModel(database.TypeCode, "")
	items := m.complItems()
	if len(items) != 1 || items[0].value != chipKindCmd {
		t.Fatalf("@ menu on code = %v, want [cmd] only", items)
	}
}

func TestChipMenuItemsQueryNode(t *testing.T) {
	// a query node keeps "#"/":" but not ">"/"[[": @ offers tag/date/cmd, not file/link.
	m, _ := menuModel(database.TypeQuery, "")
	items := m.complItems()
	got := map[string]bool{}
	for _, it := range items {
		got[it.value] = true
	}
	if got[chipKindPath] || got[chipKindLink] {
		t.Errorf("@ menu on query offered file/link, want neither: %v", items)
	}
	if !got[chipKindTag] || !got[chipKindDate] || !got[chipKindCmd] {
		t.Errorf("@ menu on query missing tag/date/cmd: %v", items)
	}
}

func TestApplyChipMenuStripsAndCreatesCmd(t *testing.T) {
	m, it := menuModel(database.TypeBullets, "cm")
	it.name = "note @cm"
	m.caret = len([]rune("note @cm"))
	items := m.complItems() // [cmd]
	if len(items) != 1 || items[0].value != chipKindCmd {
		t.Fatalf("setup: items = %v, want [cmd]", items)
	}
	m.applyChipMenu(it, items)
	// the "@cm" is stripped and the cmd creator drops a "$" at the caret
	if it.name != "note $" {
		t.Fatalf("name = %q, want %q", it.name, "note $")
	}
	if m.mode != modeComplete && m.mode != modeOutline {
		t.Errorf("unexpected mode %v after cmd create", m.mode)
	}
}

func TestApplyChipMenuDateOpensCompleter(t *testing.T) {
	m, it := menuModel(database.TypeBullets, "date")
	it.name = "note @date"
	m.caret = len([]rune("note @date"))
	items := m.complItems() // [date]
	if len(items) != 1 || items[0].value != chipKindDate {
		t.Fatalf("setup: items = %v, want [date]", items)
	}
	m.applyChipMenu(it, items)
	if it.name != "note " {
		t.Fatalf("name = %q, want %q (@date stripped)", it.name, "note ")
	}
	if m.mode != modeComplete || m.compl.kind != complDate {
		t.Fatalf("after @date, mode=%v kind=%v, want modeComplete/complDate", m.mode, m.compl.kind)
	}
}

func TestDateComplItemsParse(t *testing.T) {
	m := &Model{compl: complState{kind: complDate, query: "2025-02-11"}}
	items := m.dateComplItems()
	if len(items) == 0 || items[0].value != "2025-02-11" {
		t.Fatalf("date parse '2025-02-11' = %v, want canonical first", items)
	}
}

func TestDateComplItemsRelative(t *testing.T) {
	m := &Model{compl: complState{kind: complDate, query: "tomor"}}
	items := m.dateComplItems()
	if len(items) != 1 || items[0].label != "tomorrow" {
		t.Fatalf("date relative 'tomor' = %v, want [tomorrow]", items)
	}
}

func TestApplyCompletionDateInsertsChip(t *testing.T) {
	it := &item{uuid: "n", typ: database.TypeBullets, name: "due tomorrow"}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{
		tree:   tr,
		rows:   []row{{it: it}},
		cursor: 0,
		caret:  len([]rune("due tomorrow")),
		chips:  map[string]database.Chip{},
	}
	m.compl = complState{kind: complDate, start: len([]rune("due ")), query: "tomorrow"}
	m.applyCompletion(it, m.complItems())
	if !hasAnchor(it.name) {
		t.Fatalf("@date completion should splice a chip anchor, got %q", it.name)
	}
	for _, c := range m.chips {
		if c.Kind != chipKindDate {
			t.Errorf("created chip kind = %q, want date", c.Kind)
		}
	}
}
