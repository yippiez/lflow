package editor

import (
	"bytes"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	imagelib "image"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/zotero"
)

// fakeDetails is one fully-furnished entry: two tags (one colored), a PDF with
// two annotations (one of them commented), and a note.
func fakeDetails() *zotero.Details {
	return &zotero.Details{
		Item: zotero.Item{
			Key: "AAAA1111", Type: "journalArticle",
			Creators: []string{"Vaswani", "Shazeer"}, Year: "2017",
			Title: "Attention is all you need", Journal: "NeurIPS", DOI: "10.1000/attn",
		},
		Date:     "June 12, 2017",
		Abstract: "The dominant sequence transduction models are based on recurrent networks.",
		Tags: []zotero.Tag{
			{Name: "transformers", Color: "#ffd400"},
			{Name: "nlp"},
		},
		Attachments: []zotero.Attachment{{
			Key: "PDF00001", Title: "paper.pdf", ContentType: "application/pdf",
			Path: "/zot/storage/PDF00001/paper.pdf",
			Annotations: []zotero.Annotation{
				{Key: "ANN00001", Kind: "highlight", Text: "the encoder is composed of a stack of N = 6 layers",
					Comment: "check against the diagram", Color: "#ffd400", Page: "3"},
				{Key: "ANN00002", Kind: "highlight", Text: "we propose a new simple network architecture",
					Color: "#ff6666"},
			},
		}},
		Notes: []zotero.Note{{Key: "NOTE0001", Title: "reading notes", Text: "the residual stream framing"}},
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
func pullMirror(m *Model, d *zotero.Details) *item {
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
	want := strings.Join([]string{
		"item:Vaswani & Shazeer 2017 · Attention is all you need #transformers #nlp",
		"  meta:journalArticle · NeurIPS · June 12, 2017",
		"  meta:https://doi.org/10.1000/attn",
		"  meta:The dominant sequence transduction models are based on recurrent networks.",
		"  attachment:paper.pdf",
		"    annotation:the encoder is composed of a stack of N = 6 layers  p.3",
		"      meta:check against the diagram",
		"    annotation:we propose a new simple network architecture",
		"  note:the residual stream framing",
	}, "\n")
	if got != want {
		t.Errorf("mirrored shape:\n%s\n\nwant:\n%s", got, want)
	}
	// the full reference is one keystroke away, on the root's note
	if !strings.Contains(root.note, "Attention is all you need") {
		t.Errorf("root note = %q, want the full citation", root.note)
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
	attachment := root.children[3]
	annotation := attachment.children[0]

	// no new node may be spliced among Zotero's children, from either direction
	if _, err := m.tree.insertSiblingAfter(annotation); err == nil {
		t.Error("Enter after an annotation inserted a node into the mirror")
	}
	if _, err := m.tree.insertSiblingBefore(annotation); err == nil {
		t.Error("a node was inserted before an annotation")
	}
	if _, err := m.tree.insertFirstChild(attachment); err == nil {
		t.Error("a node was inserted inside an attachment")
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
	if _, err := m.tree.duplicate(annotation); err == nil {
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
	if m.tree.reparent(outside, attachment) {
		t.Error("an outside node was moved into an attachment")
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
	annotation := root.children[3].children[0]
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
	annotations := root.children[3].children

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
	annotationUUID := root.children[3].children[0].uuid

	// the same read again changes nothing and duplicates nothing
	pullMirror(m, fakeDetails())
	if len(root.children) != before {
		t.Fatalf("a second pull left %d children, want %d", len(root.children), before)
	}
	if got := root.children[3].children[0].uuid; got != annotationUUID {
		t.Error("an unchanged annotation was rebuilt instead of updated in place")
	}

	// now Zotero loses an annotation and gains a note
	d := fakeDetails()
	d.Attachments[0].Annotations = d.Attachments[0].Annotations[:1]
	d.Notes = append(d.Notes, zotero.Note{Key: "NOTE0002", Text: "a second note"})
	pullMirror(m, d)

	att := root.children[3]
	if len(att.children) != 1 || att.children[0].uuid != annotationUUID {
		t.Errorf("the surviving annotation did not stay put: %d children", len(att.children))
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
	if h := (zoteroSource{}).header(m, &m.list); !strings.Contains(h, "mirror:") {
		t.Errorf("header = %q, want the mirror label", h)
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
		"#ffd400":  "yellow",
		"#ff6666":  "red",
		"#5fb236":  "green",
		"#2ea8e5":  "blue",
		"#a28ae5":  "purple",
		"":         "",
		"nonsense": "",
	}
	for hex, want := range cases {
		if got := zoteroNearestColor(hex); got != want {
			t.Errorf("zoteroNearestColor(%q) = %q, want %q", hex, got, want)
		}
	}
}

func TestMirrorContextIsTyped(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	if got := zoteroToContext(m, root); got.tag != "zotero-item" || !strings.Contains(got.attrs, "AAAA1111") {
		t.Errorf("item context = %+v", got)
	}
	annotation := root.children[3].children[0]
	if got := zoteroToContext(m, annotation); got.tag != "zotero-annotation" {
		t.Errorf("annotation context = %+v", got)
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
	attachment := root.children[3] // has annotations under it

	m.cursor = m.rowIndexOf(attachment)
	m.feed(tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.mode == modeConfirm {
		t.Error("ctrl+d on a locked node opened a confirmation it could never honor")
	}
	if len(root.children) != 5 || len(attachment.children) != 2 {
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
func withImageMark(path string) *zotero.Details {
	d := fakeDetails()
	d.Attachments[0].Annotations[1] = zotero.Annotation{
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
	crop := root.children[3].children[1]

	// the picture came into the outline, so it travels with the file
	blob, ok, err := database.GetBlob(m.db, crop.uuid)
	if err != nil || !ok {
		t.Fatalf("no blob for the crop: ok=%v err=%v", ok, err)
	}
	if blob.Mime != "image/png" || blob.W != 2 || blob.H != 2 {
		t.Errorf("blob = %s %dx%d", blob.Mime, blob.W, blob.H)
	}
	if !zoteroHasImage(crop) {
		t.Error("the crop is not known to carry a picture")
	}
	// and it wears the image mark rather than the quote bar, in its own color
	glyph, col := zoteroGlyph(crop)
	if glyph != zoteroImageMark {
		t.Errorf("crop glyph = %q, want the image mark", glyph)
	}
	if col != styleColorCode["purple"] {
		t.Errorf("crop glyph color = %q, want Zotero's purple", col)
	}
	// a textual highlight is untouched by any of this
	highlight := root.children[3].children[0]
	if zoteroHasImage(highlight) {
		t.Error("a textual highlight was given a picture")
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
	crop := root.children[3].children[1]

	// the picture hangs beneath the row as half-blocks (a real row, so the tree
	// rail it hangs from is the one the outline actually drew)
	r := m.rows[m.rowIndexOf(crop)]
	if got := m.zoteroImageBands(r, false, 80); len(got) != 1 {
		t.Errorf("compact preview gave %d band lines, want a one-row strip", len(got))
	}
	m.setSetting("image.preview", "true")
	if got := m.zoteroImageBands(r, false, 80); len(got) < 1 {
		t.Error("true preview gave no thumbnail")
	}
	// a row with no picture hangs nothing
	if got := m.zoteroImageBands(m.rows[m.rowIndexOf(root)], false, 80); got != nil {
		t.Errorf("the title row grew bands: %v", got)
	}

	// alt+e expands the picture; on everything else in the mirror it declines
	if !(zoteroView{}).Enter(m, crop) {
		t.Error("alt+e declined on a crop")
	}
	if (zoteroView{}).Enter(m, root) {
		t.Error("alt+e opened an image view on the title row")
	}
	// and the flash menu names it for what it is
	verbs := ""
	for _, a := range zoteroFlashActions(m, crop) {
		verbs += a.verb + " "
	}
	if !strings.Contains(verbs, "crop") {
		t.Errorf("flash verbs = %q, want a crop action", verbs)
	}
}

func TestMirrorSkipsAnUnreadablePicture(t *testing.T) {
	m := mirrorModel(t)
	// a path that is not there, and one that is not an image
	notThere := filepath.Join(t.TempDir(), "missing.png")
	root := pullMirror(m, withImageMark(notThere))
	crop := root.children[3].children[1]
	if zoteroHasImage(crop) {
		t.Error("a missing picture was mirrored anyway")
	}

	junk := filepath.Join(t.TempDir(), "junk.png")
	if err := os.WriteFile(junk, []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	pullMirror(m, withImageMark(junk))
	if zoteroHasImage(root.children[3].children[1]) {
		t.Error("a file that is not a picture was mirrored")
	}
	// the mark itself still mirrors — just without a picture
	if got := root.children[3].children[1].name; got != "figure 1  p.4" {
		t.Errorf("crop row = %q", got)
	}
}

func TestMirrorNoteIsAnOrdinaryBullet(t *testing.T) {
	m := mirrorModel(t)
	root := pullMirror(m, fakeDetails())
	note := root.children[4]

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
