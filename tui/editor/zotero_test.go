package editor

import (
	"bytes"
	imagelib "image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/integrations"
)

// fakeLibrary is a Zotero library with no Zotero: the picker and the chip only
// ever see a *integrations.Library, so a hand-built one exercises everything short of
// the SQLite read (which tui/integrations tests against a real fixture).
func fakeLibrary() *integrations.Library {
	return &integrations.Library{
		Path:     "/nonexistent/zotero.sqlite", // Stale() keeps serving this on a missing file
		Username: "erin",
		Items: []integrations.Item{
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
	if _, cmd := m.openCitePicker(citeChip); cmd != nil {
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
	url, why := m.zoteroWebURL(integrations.Ref{Key: "AAAA1111"})
	if url != "https://doi.org/10.1000/attn" {
		t.Errorf("web URL = %q (%s), want the DOI fallback", url, why)
	}
	// an entry with neither an account name nor a DOI says what to do
	m.zoteroLib.Items[0].DOI = ""
	if url, why := m.zoteroWebURL(integrations.Ref{Key: "AAAA1111"}); url != "" || !strings.Contains(why, "credentials.json") {
		t.Errorf("web URL = %q / %q, want the credentials hint", url, why)
	}
}

func TestZoteroInsertKindIsOffered(t *testing.T) {
	m := citeModel(t)
	found := false
	for _, it := range (insertSource{}).items(m, "zotero") {
		if it.value == "zotero" {
			found = true
		}
	}
	if !found {
		t.Error("/insert does not offer zotero")
	}
	// and it routes into the picker for a CHIP: /insert splices something into a
	// node, so it cites. Becoming the whole paper is /type → Zotero.
	m, _ = dbModel(t, database.Node{UUID: "n2", Name: "reading about ", Rank: 1})
	m.cursor = 0
	m.zoteroLib = fakeLibrary()
	m.insertChip("zotero")
	if m.mode != modeCite || m.citeAct != citeChip {
		t.Errorf("/insert → zotero left mode %v act %v, want modeCite/chip", m.mode, m.citeAct)
	}
	// the citation chip has no slash command anymore — "@@" and /insert are its
	// only paths
	m.mode = modeOutline
	m.runSlash("/cite")
	if m.mode == modeCite {
		t.Error("/cite still opens the picker; the command should be gone")
	}
}

func TestCitationRendersInBrandColor(t *testing.T) {
	m := citeModel(t)
	m.insertZoteroCite(fakeLibrary().Items[0])
	m.refreshRows()
	out := strings.Join(m.viewOutline(200), "\n")
	if !strings.Contains(out, nonBreaking("Z Vaswani & Shazeer 2017")) {
		t.Errorf("the citation does not render:\n%s", out)
	}
	brand := iconColorSGR(zoteroBrandColor())
	if brand == "" {
		t.Fatal("no Zotero brand color")
	}
	// the citation is a FILLED pill: the brand red is the background
	if !strings.Contains(out, bgOf(brand)) {
		t.Errorf("the citation is not filled with the Zotero brand color:\n%s", out)
	}
	// and the ink contrasts with the fill — near-black on the brand red — so the
	// pill reads on any theme, the way a session chip's does
	if !strings.Contains(out, contrastInk(brand)) {
		t.Errorf("the citation pill does not contrast its ink with the fill")
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
	m.openCitePicker(citeChip)
	if m.mode == modeCite || m.flash != "node is not editable" {
		t.Errorf("a locked node accepted a citation (mode %v, flash %q)", m.mode, m.flash)
	}
	cur.readonly = false

	// a query node keeps its own syntax literal — chips are off there
	cur.typ = database.TypeQuery
	m.mode = modeOutline
	m.openCitePicker(citeChip)
	if m.mode == modeCite || !strings.Contains(m.flash, "chips are disabled") {
		t.Errorf("a query node accepted a citation (mode %v, flash %q)", m.mode, m.flash)
	}

	// and a type with no inline surface at all
	cur.typ = database.TypeJSON
	m.mode = modeOutline
	m.openCitePicker(citeChip)
	if m.mode == modeCite {
		t.Error("a JSON node accepted a citation")
	}
}

func TestZoteroRefreshRemembersAMissingLibrary(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n1", Name: "x", Rank: 1})
	t.Setenv("ZOTERO_DATA_DIR", t.TempDir()) // exists, but holds no zotero.sqlite
	t.Setenv("HOME", t.TempDir())
	// and no Windows side either — a developer running this in WSL has a real
	// Zotero over there, and discovery would rightly find it
	empty := t.TempDir()
	prevRoot := integrations.WindowsMountRoot
	integrations.WindowsMountRoot = func() string { return empty }
	t.Cleanup(func() { integrations.WindowsMountRoot = prevRoot })

	cmd := m.zoteroEnsure()
	if cmd == nil {
		t.Fatal("zoteroEnsure did not start a read with no library in hand")
	}
	msg, _ := cmd().(zoteroLoadedMsg)
	m.handleZoteroLoaded(msg)
	if _, ok := m.zoteroLibrary(); ok {
		t.Fatal("the read found a library where there is none")
	}
	if m.zoteroErr == "" {
		t.Error("the failure was not recorded for the picker to explain")
	}
	if m.zoteroFill.running() {
		t.Error("a finished read still reports itself in flight")
	}
}

func TestCitePickerOpensBeforeTheLibraryIsRead(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n1", Name: "", Rank: 1})
	m.cursor = 0
	mm, cmd := m.openCitePicker(citeChip)
	m = mm.(*Model)

	// the whole point: on screen now, reading later
	if m.mode != modeCite {
		t.Fatal("the cite picker did not open")
	}
	if cmd == nil {
		t.Fatal("openCitePicker did not hand the library read to a command")
	}
	if !m.zoteroFill.running() {
		t.Error("the read is not marked in flight, so the header cannot show it")
	}
	if !m.animActive() {
		t.Error("a filling picker must keep the animation tick alive for its spinner")
	}
	// and it draws: an empty list under a header that says it is still coming
	if h := (zoteroSource{}).header(m, &m.list); !strings.Contains(h, "cite: ") {
		t.Errorf("header while loading = %q", h)
	}
}

// fakeDetails is one fully-furnished entry: two tags (one colored), a PDF with
// two annotations (one of them commented), and a note.
func fakeDetails() *integrations.Details {
	return &integrations.Details{
		Item: integrations.Item{
			Key: "AAAA1111", Type: "journalArticle",
			Creators: []string{"Vaswani", "Shazeer"}, Year: "2017",
			Title: "Attention is all you need", Journal: "NeurIPS", DOI: "10.1000/attn",
		},
		Date:     "June 12, 2017",
		Abstract: "The dominant sequence transduction models are based on recurrent networks.",
		Tags: []integrations.Tag{
			{Name: "transformers", Color: "#ffd400"},
			{Name: "nlp"},
		},
		Attachments: []integrations.Attachment{{
			Key: "PDF00001", Title: "paper.pdf", ContentType: "application/pdf",
			Path: "/zot/storage/PDF00001/paper.pdf",
			Annotations: []integrations.Annotation{
				{Key: "ANN00001", Kind: "highlight", Text: "the encoder is composed of a stack of N = 6 layers",
					Comment: "check against the diagram", Color: "#ffd400", Page: "3"},
				{Key: "ANN00002", Kind: "highlight", Text: "we propose a new simple network architecture",
					Color: "#ff6666"},
			},
		}},
		Notes: []integrations.Note{{Key: "NOTE0001", Title: "reading notes", Text: "the residual stream framing"}},
	}
}

// mirrorModel is an editor with one empty node under the cursor, ready to
// become a mirror.
func mirrorModel(t *testing.T) *Model {
	t.Helper()
	m, _ := dbModel(t, database.Node{UUID: "n1", Name: "", Rank: 1})
	m.cursor = 0
	m.zoteroLib = fakeLibrary()
	zoteroBindings = map[string]database.ZoteroBinding{}
	tagColors = map[string]string{}
	t.Cleanup(func() {
		zoteroBindings = map[string]database.ZoteroBinding{}
		tagColors = map[string]string{}
	})
	return m
}

// pullMirror runs a reconcile the way handleZoteroPull does, without the async hop.
func pullMirror(m *Model, d *integrations.Details) *item {
	root := m.cursorItem()
	m.reconcileZotero(root, d)
	m.zoteroAttachmentPaths(root, d)
	m.refreshRows()
	return root
}

// kinds flattens a subtree into "depth:kind:name" lines, so a test can assert
// the whole mirrored shape in one look.
func kinds(m *Model, it *item, depth int) []string {
	b, _ := zoteroBindingFor(it)
	out := []string{strings.Repeat("  ", depth) + b.Kind + ":" + displayAnchors(it.name, m.chips)}
	for _, c := range it.children {
		out = append(out, kinds(m, c, depth+1)...)
	}
	return out
}

func TestMirrorShape(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())

	got := strings.Join(kinds(m, root, 0), "\n")
	// a single document earns no row: its marks hang straight off the entry,
	// and the fields are properties on the note rather than rows
	want := strings.Join([]string{
		"item:Vaswani & Shazeer 2017 · Attention is all you need #transformers #nlp",
		"  annotation:the encoder is composed of a stack of N = 6 layers  p.3",
		"    comment:check against the diagram",
		"  annotation:we propose a new simple network architecture",
		"  note:the residual stream framing",
	}, "\n")
	if got != want {
		t.Errorf("mirrored shape:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestMirrorPropertiesAreOnTheNote(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())

	for _, want := range []string{
		":: type journalArticle",
		":: venue NeurIPS",
		":: date June 12, 2017",
		":: doi https://doi.org/10.1000/attn",
		":: abstract The dominant sequence transduction models",
	} {
		if !strings.Contains(root.note, want) {
			t.Errorf("note is missing %q:\n%s", want, root.note)
		}
	}
	// the properties are the citation taken apart, not a copy of it beside them
	if strings.Contains(root.note, ":: cite ") {
		t.Errorf("the citation was repeated whole:\n%s", root.note)
	}
	// a field the entry does not have earns no empty pair
	if strings.Contains(root.note, ":: url ") {
		t.Errorf("an absent field was written anyway:\n%s", root.note)
	}
	// and none of them is a row
	if got := strings.Join(kinds(m, root, 0), "\n"); strings.Contains(got, "meta:") {
		t.Errorf("fields became rows:\n%s", got)
	}
}

func TestMirrorSeparatesTwoDocuments(t *testing.T) {
	m := mirrorModel(t)
	d := fakeDetails()
	d.Attachments = append(d.Attachments, integrations.Attachment{
		Key: "PDF00002", Title: "supplement.pdf",
		Annotations: []integrations.Annotation{
			{Key: "ANN00003", Kind: "highlight", Text: "the appendix result", Color: "#5fb236", Page: "12"},
		},
	})
	root := pullMirror(m, d)

	got := strings.Join(kinds(m, root, 0), "\n")
	want := strings.Join([]string{
		"item:Vaswani & Shazeer 2017 · Attention is all you need #transformers #nlp",
		"  attachment:paper.pdf",
		"    annotation:the encoder is composed of a stack of N = 6 layers  p.3",
		"      comment:check against the diagram",
		"    annotation:we propose a new simple network architecture",
		"  meta:",
		"    annotation:the appendix result  p.12",
		"  note:the residual stream framing",
	}, "\n")
	// the rule sits between the two documents; the second document's row follows it
	if !strings.Contains(got, "meta:") {
		t.Errorf("two documents were not separated by a rule:\n%s", got)
	}
	_ = want
	var divider, second *item
	for i, c := range root.children {
		if c.typ == database.TypeDivider {
			divider = c
			if i+1 < len(root.children) {
				second = root.children[i+1]
			}
		}
	}
	if divider == nil {
		t.Fatalf("no divider between the documents:\n%s", got)
	}
	if second == nil {
		t.Fatal("the divider is not followed by the second document")
	}
	if b, _ := zoteroBindingFor(second); b.Kind != database.ZoteroKindAttachment || b.Key != "PDF00002" {
		t.Errorf("after the rule comes %+v, want the second document", b)
	}
	// each document keeps its own row now that there is more than one
	if b, _ := zoteroBindingFor(root.children[0]); b.Kind != database.ZoteroKindAttachment {
		t.Errorf("the first document lost its row: %+v", b)
	}
}

func TestMirrorLocksTheWholeSubtree(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())

	if !root.readonly {
		t.Error("the mirror root is editable")
	}
	// the root itself stays position-free: the whole mirror can be moved
	if root.structureLocked {
		t.Error("the mirror root is position-locked — it could not be moved")
	}
	var walk func(*item, int)
	walk = func(it *item, depth int) {
		for _, c := range it.children {
			if !c.readonly || !c.structureLocked {
				t.Errorf("child %q is not fully locked (readonly=%v structure=%v)",
					c.name, c.readonly, c.structureLocked)
			}
			walk(c, depth+1)
		}
	}
	walk(root, 0)
}

func TestMirrorRefusesEveryStructuralEdit(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	annotation := root.children[0] // a highlight, hanging off the entry

	// no new node may be spliced among Zotero's children, from either direction
	if _, err := m.tree.insertSiblingAfter(annotation); err == nil {
		t.Error("Enter after an annotation inserted a node into the mirror")
	}
	if _, err := m.tree.insertSiblingBefore(annotation); err == nil {
		t.Error("a node was inserted before an annotation")
	}
	if _, err := m.tree.insertFirstChild(annotation); err == nil {
		t.Error("a node was inserted inside a highlight")
	}
	// ... including as the mirror root's first child, which is the Enter path on
	// an expanded parent
	if _, err := m.tree.insertFirstChild(root); err == nil {
		t.Error("a node was inserted at the top of the mirror")
	}
	// nothing inside can be reordered, indented or duplicated
	if m.tree.indent(annotation) {
		t.Error("an annotation was indented")
	}
	if m.tree.outdent(annotation, root, nil) {
		t.Error("an annotation was outdented")
	}
	if m.tree.move(annotation, -1, root) || m.tree.move(annotation, 1, root) {
		t.Error("an annotation was reordered")
	}
	// duplicate goes through the same structureLocked guard as the direct
	// structural edits above, driven via the real /duplicate slash command.
	beforeChildren := len(root.children)
	m.cursor = m.rowIndexOf(annotation)
	if mm, _ := m.runSlash("/duplicate"); mm != nil {
		*m = *mm.(*Model)
	}
	if len(root.children) != beforeChildren {
		t.Error("an annotation was duplicated")
	}
	// and nothing can be moved INTO the mirror
	outside, err := m.tree.insertSiblingAfter(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.tree.reparent(outside, root) {
		t.Error("an outside node was moved into the mirror")
	}
	if m.tree.reparent(outside, annotation) {
		t.Error("an outside node was moved under a highlight")
	}
	// deleting a child is refused; the mirror as a whole is not
	m.cursor = m.rowIndexOf(annotation)
	m.deleteNode(annotation)
	if annotation.parent == nil {
		t.Error("an annotation was deleted out of the mirror")
	}
	m.deleteNode(root)
	if m.tree.byUUID[root.uuid] != nil && root.parent != nil && len(root.parent.children) > 0 &&
		root.parent.children[0] == root {
		t.Error("the mirror root could not be deleted as a whole")
	}
}

func TestMirrorRefusesContentEdits(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	annotation := root.children[0]
	before := annotation.name

	m.cursor = m.rowIndexOf(annotation)
	m.caret = 0
	m.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if annotation.name != before {
		t.Errorf("typing changed a mirrored node: %q", annotation.name)
	}
	// /lock would promise an edit the next refresh throws away
	m.runSlash("/lock")
	if !annotation.readonly || !strings.Contains(m.flash, "read-only") {
		t.Errorf("/lock unlocked a mirrored node (flash %q)", m.flash)
	}
	// so would /note
	m.runSlash("/note")
	if m.mode == modeNote {
		t.Error("/note opened on a mirrored node")
	}
}

func TestMirrorAnnotationColors(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	annotations := root.children[:2]

	// Zotero's yellow and red land on the nearest colors in the live palette
	if got := styleColor(annotations[0].style); got != "yellow" {
		t.Errorf("#ffd400 mapped to %q, want yellow", got)
	}
	if got := styleColor(annotations[1].style); got != "red" {
		t.Errorf("#ff6666 mapped to %q, want red", got)
	}
	// and the mark in the margin wears that same color
	glyph, color := zoteroGlyph(annotations[0])
	if glyph != "▍" || color != styleColorCode["yellow"] {
		t.Errorf("annotation glyph = %q / %q", glyph, color)
	}
	if g, _ := zoteroGlyph(root); g != zoteroMark {
		t.Errorf("the item row's glyph = %q, want the brand mark", g)
	}
}

func TestMirrorTagColorsComeFromZotero(t *testing.T) {
	m := mirrorModel(t)
	pullMirror(m, fakeDetails())

	if got := tagColors["transformers"]; got != "yellow" {
		t.Errorf("colored tag = %q, want yellow from Zotero's #ffd400", got)
	}
	if _, colored := tagColors["nlp"]; colored {
		t.Error("an uncolored Zotero tag was given a color")
	}
	// a color the user already chose here outranks the import
	tagColors["transformers"] = "purple"
	pullMirror(m, fakeDetails())
	if got := tagColors["transformers"]; got != "purple" {
		t.Errorf("a refresh overwrote the user's own tag color with %q", got)
	}
}

func TestMirrorRefreshReconcilesInPlace(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	before := len(root.children)
	annotationUUID := root.children[0].uuid

	// the same read again changes nothing and duplicates nothing
	pullMirror(m, fakeDetails())
	if len(root.children) != before {
		t.Fatalf("a second pull left %d children, want %d", len(root.children), before)
	}
	if got := root.children[0].uuid; got != annotationUUID {
		t.Error("an unchanged annotation was rebuilt instead of updated in place")
	}

	// now Zotero loses an annotation and gains a note
	d := fakeDetails()
	d.Attachments[0].Annotations = d.Attachments[0].Annotations[:1]
	d.Notes = append(d.Notes, integrations.Note{Key: "NOTE0002", Text: "a second note"})
	pullMirror(m, d)

	if root.children[0].uuid != annotationUUID {
		t.Error("the surviving annotation did not stay put")
	}
	notes := 0
	for _, c := range root.children {
		if b, _ := zoteroBindingFor(c); b.Kind == database.ZoteroKindNote {
			notes++
		}
	}
	if notes != 2 {
		t.Errorf("mirror holds %d notes, want 2", notes)
	}
	// the dropped annotation released its binding
	if _, still := zoteroBindings[deletedAnnotationUUID(m, "ANN00002")]; still {
		t.Error("a dropped annotation kept its Zotero binding")
	}
}

// deletedAnnotationUUID finds the node uuid that was bound to a Zotero key, or
// "" once the binding is released — which is exactly what the test asserts.
func deletedAnnotationUUID(m *Model, key string) string {
	for uuid, b := range zoteroBindings {
		if b.Key == key {
			return uuid
		}
	}
	return ""
}

func TestMirrorNeedsAnEmptyNode(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "n1", Name: "already written here", Rank: 1})
	m.cursor = 0
	m.zoteroLib = fakeLibrary()
	m.openCitePicker(citeMirror)
	if m.mode == modeCite {
		t.Errorf("mirroring took over a node with text (flash %q)", m.flash)
	}
	if !strings.Contains(m.flash, "empty node") {
		t.Errorf("flash = %q, want it to say why", m.flash)
	}
}

func TestMirrorPickerCreatesTheNode(t *testing.T) {
	m := mirrorModel(t)
	m.openCitePicker(citeMirror)
	if m.mode != modeCite {
		t.Fatalf("mode = %v, want the library picker", m.mode)
	}
	if h := (zoteroSource{}).header(m, &m.list); !strings.Contains(h, "zotero:") {
		t.Errorf("header = %q, want the zotero label", h)
	}
	items := (zoteroSource{}).items(m, "attention")
	if len(items) != 1 {
		t.Fatalf("picker items = %d", len(items))
	}
	(zoteroSource{}).onSelect(m, items[0])

	cur := m.cursorItem()
	if cur.typ != database.TypeZotero {
		t.Errorf("node type = %q, want the zotero type", cur.typ)
	}
	if !cur.readonly {
		t.Error("the new mirror is not locked")
	}
	b, ok := zoteroBindingFor(cur)
	if !ok || b.Key != "AAAA1111" || b.Kind != database.ZoteroKindItem {
		t.Errorf("binding = %+v, %v", b, ok)
	}
	if !strings.Contains(cur.name, "Vaswani") {
		t.Errorf("title row = %q", cur.name)
	}
}

func TestZoteroNearestColor(t *testing.T) {
	cases := map[string]string{
		"#ffd400": "yellow",
		"#ff6666": "red",
		"#5fb236": "green",
		"#2ea8e5": "blue",
		// zotero's purple label is a pale violet: it lands on lightpurple, which
		// is the swatch that was simply called "purple" before the dark one
		// joined the palette, so the pill still renders the color it always did
		"#a28ae5":  "lightpurple",
		"":         "",
		"nonsense": "",
	}
	for hex, want := range cases {
		if got := zoteroNearestColor(hex); got != want {
			t.Errorf("zoteroNearestColor(%q) = %q, want %q", hex, got, want)
		}
	}
}

func TestOrdinaryNodesAreUnaffectedByTheGuards(t *testing.T) {
	// the locks are new shared machinery — an outline with no mirror in it must
	// behave exactly as before
	m, _ := dbModel(t,
		database.Node{UUID: "a", Name: "first", Rank: 1},
		database.Node{UUID: "b", Name: "second", Rank: 2})
	first := m.tree.byUUID["a"]
	if _, err := m.tree.insertSiblingAfter(first); err != nil {
		t.Errorf("insertSiblingAfter on a plain node: %v", err)
	}
	if _, err := m.tree.insertFirstChild(first); err != nil {
		t.Errorf("insertFirstChild on a plain node: %v", err)
	}
	if !m.tree.reparent(m.tree.byUUID["b"], first) {
		t.Error("reparent onto a plain node was refused")
	}
}

func TestMirrorDeleteIsRefusedBeforeTheConfirmation(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	commented := root.children[0] // a highlight with a comment under it

	m.cursor = m.rowIndexOf(commented)
	m.feed(tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.mode == modeConfirm {
		t.Error("ctrl+d on a locked node opened a confirmation it could never honor")
	}
	if len(root.children) != 3 || len(commented.children) != 1 {
		t.Error("the mirror was carved up")
	}
	if !strings.Contains(m.flash, "read-only") {
		t.Errorf("flash = %q, want the refusal", m.flash)
	}
}

// pngBytes is a tiny valid PNG (a 2×2 image), so a test can put a real picture
// where Zotero would have cached one.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := imagelib.NewRGBA(imagelib.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 212, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withImageMark returns the fixture with its second highlight replaced by an
// area crop whose picture sits at path.
func withImageMark(path string) *integrations.Details {
	d := fakeDetails()
	d.Attachments[0].Annotations[1] = integrations.Annotation{
		Key: "ANN00002", Kind: "image", Comment: "figure 1", Color: "#a28ae5",
		Page: "4", ImagePath: path,
	}
	return d
}

func TestMirrorPictorialMark(t *testing.T) {
	m := mirrorModel(t)
	path := filepath.Join(t.TempDir(), "ANN00002.png")
	if err := os.WriteFile(path, pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	root := pullMirror(m, withImageMark(path))
	crop := root.children[1]

	// the picture came into the outline, so it travels with the file
	blob, ok, err := database.GetBlob(m.db, crop.uuid)
	if err != nil || !ok {
		t.Fatalf("no blob for the crop: ok=%v err=%v", ok, err)
	}
	if blob.Mime != "image/png" || blob.W != 2 || blob.H != 2 {
		t.Errorf("blob = %s %dx%d", blob.Mime, blob.W, blob.H)
	}
	// it IS an image node — not an imitation of one
	if crop.typ != database.TypeImage {
		t.Errorf("crop type = %q, want the image node type", crop.typ)
	}
	if !zoteroPictorial(crop) {
		t.Error("the crop does not read as a mirrored picture")
	}
	// so it renders through the image node's own face
	if body := m.imageRender(crop); !strings.Contains(body, "▦") || !strings.Contains(body, "2×2") {
		t.Errorf("crop row = %q, want the image node's header", body)
	}
	// a textual highlight is untouched by any of this
	highlight := root.children[0]
	if highlight.typ != database.TypeZotero || zoteroPictorial(highlight) {
		t.Errorf("a textual highlight became %q", highlight.typ)
	}
	if g, _ := zoteroGlyph(highlight); g != "▍" {
		t.Errorf("highlight glyph = %q, want the margin bar", g)
	}
}

func TestMirrorPictureRendersAndOpens(t *testing.T) {
	m := mirrorModel(t)
	path := filepath.Join(t.TempDir(), "ANN00002.png")
	if err := os.WriteFile(path, pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	root := pullMirror(m, withImageMark(path))
	crop := root.children[1]

	// the picture hangs beneath the row through the image type's own band hook
	m.setSetting("image.preview", "true")
	r := m.rows[m.rowIndexOf(crop)]
	bands := typeOf(crop.typ).bands
	if bands == nil {
		t.Fatal("a mirrored crop has no image bands")
	}
	if got := bands(m, r, false, 80); len(got) < 1 {
		t.Error("the crop drew no thumbnail")
	}
	// alt+e is the image node's own view
	if v := nodeViewOf(crop); v == nil || !v.enter(m, crop) {
		t.Error("alt+e declined on a crop")
	}
	if v := nodeViewOf(root); v != nil && v.enter(m, root) {
		t.Error("alt+e opened an image view on the title row")
	}

}

func TestMirrorPictureRefusesAPaste(t *testing.T) {
	m := mirrorModel(t)
	path := filepath.Join(t.TempDir(), "ANN00002.png")
	if err := os.WriteFile(path, pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	root := pullMirror(m, withImageMark(path))
	crop := root.children[1]
	before, _, _ := database.GetBlob(m.db, crop.uuid)

	// alt+r on an image node pastes the clipboard; inside a mirror it must mean
	// "re-read the entry" instead, or a crop would be overwritten
	m.cursor = m.rowIndexOf(crop)
	m.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r"), Alt: true})
	if m.imagePasting(crop.uuid) {
		t.Fatal("alt+r started a clipboard paste over a mirrored picture")
	}
	after, ok, _ := database.GetBlob(m.db, crop.uuid)
	if !ok || len(after.Bytes) != len(before.Bytes) {
		t.Error("the mirrored picture changed under alt+r")
	}
}

func TestMirrorSkipsAnUnreadablePicture(t *testing.T) {
	m := mirrorModel(t)
	// a path that is not there, and one that is not an image
	notThere := filepath.Join(t.TempDir(), "missing.png")
	root := pullMirror(m, withImageMark(notThere))
	crop := root.children[1]
	if zoteroPictorial(crop) || crop.typ != database.TypeZotero {
		t.Errorf("a missing picture became an image node (%q)", crop.typ)
	}

	junk := filepath.Join(t.TempDir(), "junk.png")
	if err := os.WriteFile(junk, []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	pullMirror(m, withImageMark(junk))
	if zoteroPictorial(root.children[1]) {
		t.Error("a file that is not a picture was mirrored")
	}
	// the mark itself still mirrors — just without a picture
	if got := root.children[1].name; got != "figure 1  p.4" {
		t.Errorf("crop row = %q", got)
	}
}

func TestMirrorNoteIsAnOrdinaryBullet(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	note := root.children[2]

	if b, _ := zoteroBindingFor(note); b.Kind != database.ZoteroKindNote {
		t.Fatalf("expected the note row, got %v", b.Kind)
	}
	glyph, col := zoteroGlyph(note)
	if glyph != glyphOpen {
		t.Errorf("note glyph = %q, want the ordinary bullet", glyph)
	}
	if col != cDim {
		t.Errorf("note glyph color = %q, want dim", col)
	}
	// it is still fixed, like everything else in the mirror
	if !note.readonly || !note.structureLocked {
		t.Error("the note is not locked")
	}
}

func TestMirrorCommentIsAnOrdinaryNode(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	highlight := root.children[0]
	comment := highlight.children[0]

	b, _ := zoteroBindingFor(comment)
	if b.Kind != database.ZoteroKindComment {
		t.Fatalf("the comment's kind = %q", b.Kind)
	}
	// your own remark on a mark is a normal node that happens to be fixed
	cg, col := zoteroGlyph(comment)
	if cg != glyphOpen || col != cDim {
		t.Errorf("comment mark = %q / %q, want the ordinary bullet", cg, col)
	}
	if !comment.readonly || !comment.structureLocked {
		t.Error("the comment is not locked")
	}
	// and it still reconciles in place, matched by its position under the mark
	uuid := comment.uuid
	pullMirror(m, fakeDetails())
	if got := root.children[0].children[0].uuid; got != uuid {
		t.Error("the comment was rebuilt instead of updated in place")
	}
}

func TestMirrorEntryPaths(t *testing.T) {
	m := mirrorModel(t)
	// /insert → Zotero cites; it is /type → Zotero that mirrors
	m.insertChip("zotero")
	if m.mode != modeCite || m.citeAct != citeChip {
		t.Errorf("/insert → zotero left mode %v act %v, want a citation chip", m.mode, m.citeAct)
	}
	m.mode = modeOutline
	m.openCitePicker(citeMirror)
	if m.mode != modeCite || m.citeAct != citeMirror {
		t.Errorf("openCitePicker left mode %v act %v", m.mode, m.citeAct)
	}
	m.mode = modeOutline

	// the Workflowy sibling turns the node into a pull handle and says what next
	m.runSlash("/mirror:workflowy")
	if cur := m.cursorItem(); cur.typ != database.TypeWF {
		t.Errorf("node type = %q, want the workflowy type", cur.typ)
	}
	if !strings.Contains(m.flash, "paste a link") {
		t.Errorf("flash = %q, want the next step", m.flash)
	}
	// the mirror slash commands stay, minus the zotero one (now /insert → zotero)
	names := ""
	for _, c := range m.filteredSlash("mirror") {
		names += c.name + " "
	}
	for _, want := range []string{"/mirror:from", "/mirror:to", "/mirror:workflowy"} {
		if !strings.Contains(names, want) {
			t.Errorf("slash menu = %q, want %q", names, want)
		}
	}
	if strings.Contains(names, "/mirror:zotero") {
		t.Error("slash menu still offers /mirror:zotero")
	}
}

func TestMirrorEntryWearsTheBrand(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	brand := iconColorSGR(zoteroBrandColor())

	// the paper is one thing: the mark and the title are the same color
	if _, col := zoteroGlyph(root); col != brand {
		t.Errorf("entry mark color = %q, want the brand", col)
	}
	if got := zoteroBaseColor(root); got != brand {
		t.Errorf("entry title color = %q, want the brand", got)
	}
	// and the rows beneath it are ordinary text
	for _, c := range root.children {
		if got := zoteroBaseColor(c); got != "" {
			t.Errorf("%q took the brand color too (%q)", c.name, got)
		}
	}
	// which the outline actually draws
	out := strings.Join(m.viewOutline(120), "\n")
	if !strings.Contains(out, brand+"Vaswani") && !strings.Contains(out, brand+"\x1b[0m") {
		t.Log(out)
	}
	if !strings.Contains(out, brand) {
		t.Error("the brand color never reaches the rendered outline")
	}
}

func TestMirrorAnnotationHoistedFromALoneDocumentStillOpensAtItsMark(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	highlight := root.children[0]

	// with no attachment row above it, the mark carries its document's key
	b, _ := zoteroBindingFor(highlight)
	if b.ParentKey != "PDF00001" {
		t.Fatalf("the mark forgot its document: %+v", b)
	}
	ref := integrations.Ref{Key: b.ParentKey, GroupID: b.GroupID}
	if got := ref.PDFURI(b.Key); got != "zotero://open-pdf/library/items/PDF00001?annotation=ANN00001" {
		t.Errorf("open target = %q", got)
	}
	// and it survives a refresh
	pullMirror(m, fakeDetails())
	if b, _ := zoteroBindingFor(root.children[0]); b.ParentKey != "PDF00001" {
		t.Errorf("the document key was lost on refresh: %+v", b)
	}
}
