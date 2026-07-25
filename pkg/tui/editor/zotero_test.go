package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/zotero"
)

// fakeLibrary is a Zotero library with no Zotero: the picker and the chip only
// ever see a *zotero.Library, so a hand-built one exercises everything short of
// the SQLite read (which pkg/tui/zotero tests against a real fixture).
func fakeLibrary() *zotero.Library {
	return &zotero.Library{
		Path:     "/nonexistent/zotero.sqlite", // Stale() keeps serving this on a missing file
		Username: "erin",
		Items: []zotero.Item{
			{Key: "AAAA1111", Creators: []string{"Vaswani", "Shazeer"}, Year: "2017",
				Title: "Attention is all you need", Journal: "NeurIPS", DOI: "10.1000/attn"},
			{Key: "BBBB2222", GroupID: "5566", Creators: []string{"Smith"}, Year: "2020",
				Title: "Thinking in outlines"},
		},
	}
}

// citeModel is an editor with one node, a cursor on it, and a loaded library.
func citeModel(t *testing.T) *Model {
	t.Helper()
	m, _ := dbModel(t, database.Node{UUID: "n1", Name: "as shown in ", Rank: 1})
	m.cursor = 0
	m.caret = len([]rune("as shown in "))
	m.zoteroLib = fakeLibrary()
	zoteroAccount = "erin"
	zoteroOpenMode = "local"
	t.Cleanup(func() { zoteroAccount, zoteroOpenMode = "", "local" })
	return m
}

func zoteroChipOf(m *Model) (database.Chip, bool) {
	for _, c := range m.chips {
		if c.Kind == chipKindZotero {
			return c, true
		}
	}
	return database.Chip{}, false
}

func TestCitePickerInsertsAChip(t *testing.T) {
	m := citeModel(t)
	if _, cmd := m.openCitePicker(); cmd != nil {
		t.Fatal("opening the cite picker should not fire a command")
	}
	if m.mode != modeCite {
		t.Fatalf("mode = %v, want modeCite", m.mode)
	}

	items := (zoteroSource{}).items(m, "attention")
	if len(items) != 1 || items[0].value != "AAAA1111" {
		t.Fatalf("picker items for 'attention' = %+v", items)
	}
	(zoteroSource{}).onSelect(m, items[0])

	if m.mode != modeOutline {
		t.Errorf("mode after a pick = %v, want modeOutline", m.mode)
	}
	c, ok := zoteroChipOf(m)
	if !ok {
		t.Fatal("no citation chip was created")
	}
	if c.Value != "zotero://select/library/items/AAAA1111" {
		t.Errorf("chip value = %q, want the select URI", c.Value)
	}
	if c.Label != "Vaswani & Shazeer 2017" {
		t.Errorf("chip label = %q", c.Label)
	}
	cur := m.cursorItem()
	if !strings.HasPrefix(cur.name, "as shown in ") || !hasAnchor(cur.name) {
		t.Errorf("node name = %q, want the anchor spliced in after the text", cur.name)
	}
	// the caret parks just past the chip, ready to keep typing
	if m.caret != len([]rune(cur.name)) {
		t.Errorf("caret = %d, want the end of %q", m.caret, cur.name)
	}
	// and the chip is persisted, not just in memory
	if _, err := database.GetChip(m.db, c.ID); err != nil {
		t.Errorf("chip was not written to the DB: %v", err)
	}
}

func TestCitePickerEmptyQueryListsTheLibrary(t *testing.T) {
	m := citeModel(t)
	if got := (zoteroSource{}).items(m, ""); len(got) != 2 {
		t.Errorf("empty query listed %d entries, want the whole library", len(got))
	}
	if got := (zoteroSource{}).items(m, "zzz"); len(got) != 0 {
		t.Errorf("a query that matches nothing listed %d entries", len(got))
	}
}

func TestCitePickerWithoutALibrary(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n1", Name: "x", Rank: 1})
	m.zoteroErr = "no Zotero library found"
	if got := (zoteroSource{}).items(m, ""); got != nil {
		t.Errorf("items with no library = %+v, want nothing", got)
	}
	if h := (zoteroSource{}).header(m, &m.list); !strings.Contains(h, "no Zotero library found") {
		t.Errorf("header = %q, want it to explain the missing library", h)
	}
	// selecting in an empty picker is a no-op that closes cleanly
	m.mode = modeCite
	(zoteroSource{}).onSelect(m, pickerItem{})
	if m.mode != modeOutline {
		t.Error("selecting nothing should return to the outline")
	}
}

func TestDoubleAtOpensTheCitePicker(t *testing.T) {
	m := citeModel(t)
	m.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if m.mode == modeCite {
		t.Fatal("a single @ must stay literal")
	}
	if got := m.cursorItem().name; got != "as shown in @" {
		t.Fatalf("after one @ the name is %q", got)
	}
	m.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if m.mode != modeCite {
		t.Fatalf("@@ did not open the cite picker (mode %v)", m.mode)
	}
	// the trigger leaves no text behind
	if got := m.cursorItem().name; got != "as shown in " {
		t.Errorf("after @@ the name is %q, want the @ consumed", got)
	}
}

func TestChipDisplayAndExpand(t *testing.T) {
	c := database.Chip{Kind: chipKindZotero, Value: "zotero://select/library/items/AAAA1111", Label: "Vaswani & Shazeer 2017"}
	if got := chipDisplay(c); got != "Z Vaswani & Shazeer 2017" {
		t.Errorf("chipDisplay = %q", got)
	}
	if got := chipExpand(c); got != "[Vaswani & Shazeer 2017](zotero://select/library/items/AAAA1111)" {
		t.Errorf("chipExpand = %q", got)
	}
	// the same pair on the DB-level resolver the CLI uses
	if got := database.ChipDisplay(c); got != "Z Vaswani & Shazeer 2017" {
		t.Errorf("database.ChipDisplay = %q", got)
	}
	if got := database.ChipExpand(c); got != "[Vaswani & Shazeer 2017](zotero://select/library/items/AAAA1111)" {
		t.Errorf("database.ChipExpand = %q", got)
	}
	// a label-less chip still reads as something
	bare := database.Chip{Kind: chipKindZotero, Value: "zotero://select/library/items/AAAA1111"}
	if got := chipDisplay(bare); got != "Z AAAA1111" {
		t.Errorf("chipDisplay of a label-less citation = %q", got)
	}
}

func TestZoteroLinkTargetFollowsTheSetting(t *testing.T) {
	defer func() { zoteroOpenMode, zoteroAccount = "local", "" }()
	c := database.Chip{Kind: chipKindZotero, Value: "zotero://select/library/items/AAAA1111", Label: "Vaswani 2017"}

	zoteroOpenMode, zoteroAccount = "local", "erin"
	if got := zoteroLinkTarget(c); got != "zotero://select/library/items/AAAA1111" {
		t.Errorf("local target = %q", got)
	}
	zoteroOpenMode = "cloud"
	if got := zoteroLinkTarget(c); got != "https://www.zotero.org/erin/items/AAAA1111/library" {
		t.Errorf("cloud target = %q", got)
	}
	// no account name: no hyperlink rather than a broken one
	zoteroAccount = ""
	if got := zoteroLinkTarget(c); got != "" {
		t.Errorf("cloud target with no account = %q, want none", got)
	}
	// a group library needs no account name at all
	group := database.Chip{Kind: chipKindZotero, Value: "zotero://select/groups/5566/items/BBBB2222"}
	if got := zoteroLinkTarget(group); got != "https://www.zotero.org/groups/5566/items/BBBB2222/library" {
		t.Errorf("group cloud target = %q", got)
	}
	// a chip whose value is not a reference advertises nothing
	if got := zoteroLinkTarget(database.Chip{Kind: chipKindZotero, Value: "nonsense"}); got != "" {
		t.Errorf("target of a broken citation = %q", got)
	}
}

func TestZoteroChipAtCaret(t *testing.T) {
	m := citeModel(t)
	m.insertZoteroCite(fakeLibrary().Items[0])
	cur := m.cursorItem()
	spans := anchorSpans([]rune(cur.name))
	if len(spans) != 1 {
		t.Fatalf("expected one anchor, got %d", len(spans))
	}
	// the caret rests just after the chip
	if _, ok := m.zoteroChipAtCaret(cur); !ok {
		t.Error("no citation found with the caret at the anchor's end")
	}
	m.caret = spans[0].start
	if _, ok := m.zoteroChipAtCaret(cur); !ok {
		t.Error("no citation found with the caret at the anchor's start")
	}
	m.caret = 0
	if _, ok := m.zoteroChipAtCaret(cur); ok {
		t.Error("a citation was found with the caret nowhere near it")
	}
	// a link chip at the caret is not a citation
	m.caret = len([]rune(cur.name))
	m.insertLinkChip("https://example.com", "example")
	if _, ok := m.zoteroChipAtCaret(cur); ok {
		t.Error("a link chip was mistaken for a citation")
	}
}

func TestOpenZoteroChipRejectsABrokenReference(t *testing.T) {
	m := citeModel(t)
	m.openZoteroChip(database.Chip{Kind: chipKindZotero, Value: "nonsense"}, true)
	if !strings.Contains(m.flash, "no library reference") {
		t.Errorf("flash = %q, want the broken-reference message", m.flash)
	}
}

func TestZoteroWebURLFallsBackToTheDOI(t *testing.T) {
	m := citeModel(t)
	m.zoteroLib.Username = ""
	m.ctx.Paths.Config = "" // and no credentials.json to read either
	url, why := m.zoteroWebURL(zotero.Ref{Key: "AAAA1111"})
	if url != "https://doi.org/10.1000/attn" {
		t.Errorf("web URL = %q (%s), want the DOI fallback", url, why)
	}
	// an entry with neither an account name nor a DOI says what to do
	m.zoteroLib.Items[0].DOI = ""
	if url, why := m.zoteroWebURL(zotero.Ref{Key: "AAAA1111"}); url != "" || !strings.Contains(why, "credentials.json") {
		t.Errorf("web URL = %q / %q, want the credentials hint", url, why)
	}
}

func TestCiteInsertKindIsOffered(t *testing.T) {
	m := citeModel(t)
	found := false
	for _, it := range (insertSource{}).items(m, "cite") {
		if it.value == "cite" {
			found = true
		}
	}
	if !found {
		t.Error("/insert does not offer cite")
	}
	// and it routes into the picker
	m.mode = modeInsert
	m.insertChip("cite")
	if m.mode != modeCite {
		t.Errorf("/insert → cite left mode %v, want modeCite", m.mode)
	}
}

func TestSlashCiteOpensThePicker(t *testing.T) {
	m := citeModel(t)
	m.runSlash("/cite")
	if m.mode != modeCite {
		t.Errorf("/cite left mode %v, want modeCite", m.mode)
	}
}

func TestCitationRendersInBrandColor(t *testing.T) {
	m := citeModel(t)
	m.insertZoteroCite(fakeLibrary().Items[0])
	m.refreshRows()
	out := strings.Join(m.viewOutline(200), "\n")
	if !strings.Contains(out, "Z Vaswani & Shazeer 2017") {
		t.Errorf("the citation does not render:\n%s", out)
	}
	brand := iconColorSGR(zoteroBrandColor())
	if brand == "" || !strings.Contains(out, brand) {
		t.Errorf("the citation is not painted in the Zotero brand color")
	}
	// and it advertises its destination as a terminal hyperlink
	if !strings.Contains(out, "\x1b]8;;zotero://select/library/items/AAAA1111") {
		t.Error("the citation carries no OSC 8 hyperlink")
	}
}

func TestCitePickerRefusesANodeThatCannotHoldAChip(t *testing.T) {
	m := citeModel(t)
	cur := m.cursorItem()

	cur.readonly = true
	m.openCitePicker()
	if m.mode == modeCite || m.flash != "node is not editable" {
		t.Errorf("a locked node accepted a citation (mode %v, flash %q)", m.mode, m.flash)
	}
	cur.readonly = false

	// a query node keeps its own syntax literal — chips are off there
	cur.typ = database.TypeQuery
	m.mode = modeOutline
	m.openCitePicker()
	if m.mode == modeCite || !strings.Contains(m.flash, "chips are disabled") {
		t.Errorf("a query node accepted a citation (mode %v, flash %q)", m.mode, m.flash)
	}

	// and a type with no inline surface at all
	cur.typ = database.TypeJSON
	m.mode = modeOutline
	m.openCitePicker()
	if m.mode == modeCite {
		t.Error("a JSON node accepted a citation")
	}
}

func TestZoteroRefreshRemembersAMissingLibrary(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n1", Name: "x", Rank: 1})
	t.Setenv("LFLOW_ZOTERO_DIR", t.TempDir()) // exists, but holds no zotero.sqlite
	t.Setenv("ZOTERO_DATA_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, ok := m.zoteroRefresh(); ok {
		t.Fatal("zoteroRefresh found a library where there is none")
	}
	if m.zoteroErr == "" {
		t.Error("the failure was not recorded for the picker to explain")
	}
}
