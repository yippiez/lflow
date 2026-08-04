package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// buildRenderRowFixture builds one tree exercising every row-shape branch
// renderRow has to handle identically across its three entry points
// (viewRenderRows, finalView, readonlyRegionLines): a multi-line code block, a
// real todo and a MIRROR of it, a divider, an empty spacer, a bash node with a
// run headline, and a query node with a cached run-output band (bash's own
// band is suppressed — runInTail — so the query node is what exercises
// runBandLines itself). m owns the run state the bash/query nodes read from.
func buildRenderRowFixture(m *Model) (tr *tree, todo, mirror *item) {
	root := &item{uuid: "root", typ: database.TypeBullets}
	tr = &tree{
		root:          root,
		byUUID:        map[string]*item{root.uuid: root},
		externalNames: map[string]string{},
	}
	add := func(it *item) {
		it.parent = root
		root.children = append(root.children, it)
		tr.byUUID[it.uuid] = it
	}

	code := &item{uuid: "code1", typ: database.TypeCode, name: "line one\nline two"}
	add(code)

	todo = &item{uuid: "todo1", typ: database.TypeTodo, name: "buy milk"}
	add(todo)

	// the mirror's OWN materialized row carries a placeholder type (bullets) —
	// exactly like a real mirror/query-hit row does — so its glyph can only
	// come from resolving mirrorOf to the source; a renderer that trusts the
	// raw item's type/style shows a plain bullet instead of the source's todo
	// box (see renderItem's comment: glyph/type-face/style are the source's).
	mirror = &item{uuid: "todo1-mirror", typ: database.TypeBullets, mirrorOf: "todo1"}
	add(mirror)

	div := &item{uuid: "div1", typ: database.TypeDivider}
	add(div)

	empty := &item{uuid: "empty1", typ: database.TypeEmpty}
	add(empty)

	bash := &item{uuid: "bash1", typ: database.TypeBash, name: "echo hi"}
	add(bash)
	br := m.ensureRun(bash.uuid)
	br.loaded = true
	br.out = append(br.out, outLine{text: "hi"})
	m.syncRunTails() // bash's headline lives in runTails, read by bashBodyTail

	query := &item{uuid: "query1", typ: database.TypeQuery, name: "#tag"}
	add(query)
	qr := m.ensureRun(query.uuid)
	qr.loaded = true
	qr.out = append(qr.out, outLine{text: "3 hits found"})

	return tr, todo, mirror
}

// wantParityFaces are the substrings every renderer must show somewhere in its
// output for the fixture above.
var wantParityFaces = []string{
	"1 │ line one", // (a) the code node's block, gutter line 1
	"2 │ line two", // (a) the code node's block, gutter line 2
	"─",            // the divider's rule
	"hi",           // the bash node's run headline (bodyTail)
	"3 hits found", // (b) the query node's runBandLines band
}

// TestRenderRowParityFinalView is divergence (a)+(b): the quit dump used to have
// no blockCode branch at all (a Code node collapsed to its one-line fallback)
// and never called runBandLines (a non-runInTail runnable's output vanished).
// Both must show up in the final frame now that it shares renderRow.
func TestRenderRowParityFinalView(t *testing.T) {
	m := &Model{}
	tr, todo, mirror := buildRenderRowFixture(m)
	m.tree = tr

	out := stripSGR(strings.Join(m.finalView(80), "\n"))
	for _, want := range wantParityFaces {
		if !strings.Contains(out, want) {
			t.Errorf("finalView missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "code · 2 lines") {
		t.Errorf("finalView collapsed the code block to its inline fallback:\n%s", out)
	}
	// both the real todo and its mirror resolve to the todo glyph, never a
	// plain bullet — mirrors keep the source's glyph, not their own type-check
	if got := strings.Count(out, glyphTodo); got < 2 {
		t.Errorf("finalView shows %d todo glyphs (real + mirror), want at least 2:\n%s", got, out)
	}
	_ = todo
	_ = mirror
}

// TestRenderRowParityViewRenderRows is the interactive live frame: the safety
// net that nothing regressed while unifying the three renderers.
func TestRenderRowParityViewRenderRows(t *testing.T) {
	m := &Model{width: 80, height: 40}
	tr, _, _ := buildRenderRowFixture(m)
	m.tree = tr
	m.viewStack = []*item{tr.root}
	m.refreshRows()

	groups, bands := m.viewRenderRows(79)
	var out strings.Builder
	for i := range groups {
		out.WriteString(strings.Join(groups[i], "\n"))
		out.WriteString("\n")
		out.WriteString(strings.Join(bands[i], "\n"))
		out.WriteString("\n")
	}
	plain := stripSGR(out.String())
	for _, want := range wantParityFaces {
		if !strings.Contains(plain, want) {
			t.Errorf("viewRenderRows missing %q:\n%s", want, plain)
		}
	}
	if got := strings.Count(plain, glyphTodo); got < 2 {
		t.Errorf("viewRenderRows shows %d todo glyphs (real + mirror), want at least 2:\n%s", got, plain)
	}
}

// TestRenderRowParityReadonlyRegion is divergence (c)+(d): the read-only region
// used to call renderBody/glyphFor on the raw item instead of resolving through
// shown := tr.resolve(it) — so a mirror rendered as a plain bullet there.
// m.tree is deliberately a DIFFERENT tree than the one being rendered (the
// stashed-main-outline scenario, where m.tree is the live Temporary Domain
// tree while the main outline renders read-only): it carries its own DECOY
// "todo1" — completed, so it shows the DONE glyph — so resolution through the
// wrong tree is visibly distinguishable from resolution through tr, where the
// real todo1 is still open.
func TestRenderRowParityReadonlyRegion(t *testing.T) {
	m := &Model{}
	tr, _, _ := buildRenderRowFixture(m)
	decoy := &item{uuid: "todo1", typ: database.TypeTodo, name: "decoy", completedAt: 1}
	m.tree = &tree{root: &item{uuid: "unrelated"}, byUUID: map[string]*item{"todo1": decoy}} // NOT tr

	// dashed=false, matching the real stashed-main-outline call site (dashed is
	// the TEMP PANEL's own look, which deliberately overrides a real node's
	// glyph — the mirror's glyph is the thing under test here, not that swap)
	lines := m.readonlyRegionLines(tr, tr.root, 0, 20, 80, false, 0)
	plain := stripSGR(strings.Join(lines, "\n"))
	for _, want := range wantParityFaces {
		if !strings.Contains(plain, want) {
			t.Errorf("readonlyRegionLines missing %q:\n%s", want, plain)
		}
	}
	// (c)/(d): the mirror resolves through tr (the real, open todo1), not
	// through m.tree's decoy — it shows the open glyph, never the decoy's done
	// glyph and never a plain bullet fallback
	if got := strings.Count(plain, glyphTodo); got < 2 {
		t.Errorf("readonlyRegionLines shows %d open todo glyphs (real + mirror), want at least 2 — a mirror resolved against the wrong tree or fell back to a plain bullet:\n%s", got, plain)
	}
	if strings.Contains(plain, glyphTodoDone) {
		t.Errorf("readonlyRegionLines resolved the mirror against m.tree's decoy instead of the tree it was handed:\n%s", plain)
	}
	if strings.Contains(plain, "code · 2 lines") {
		t.Errorf("readonlyRegionLines collapsed the code block to its inline fallback:\n%s", plain)
	}
}

// TestRenderRowParityBlockCodeGlyphUsesShown is divergence (d): inside
// viewRenderRows the block-code branch alone read glyphFor(it) — the raw,
// pre-resolve item — while every other branch already used glyphFor(shown).
// A collapsed source code node mirrored into an EXPANDED (childless) mirror
// row must still show the COLLAPSED glyph, because collapse/children are read
// off the resolved source, not the mirror's own copy.
func TestRenderRowParityBlockCodeGlyphUsesShown(t *testing.T) {
	m := &Model{width: 80, height: 40}
	root := &item{uuid: "root", typ: database.TypeBullets}
	tr := &tree{root: root, byUUID: map[string]*item{root.uuid: root}, externalNames: map[string]string{}}
	add := func(it *item) {
		it.parent = root
		root.children = append(root.children, it)
		tr.byUUID[it.uuid] = it
	}
	src := &item{uuid: "src", typ: database.TypeCode, name: "x = 1", collapsed: true}
	add(src)
	child := &item{uuid: "src-child", typ: database.TypeBullets, name: "note", parent: src}
	tr.byUUID[child.uuid] = child
	src.children = []*item{child}
	mirror := &item{uuid: "src-mirror", typ: database.TypeCode, mirrorOf: "src", collapsed: false}
	add(mirror)

	m.tree = tr
	m.viewStack = []*item{root}
	m.refreshRows()

	groups, _ := m.viewRenderRows(79)
	mirrorGroup := stripSGR(strings.Join(groups[1], "\n"))
	if strings.Contains(mirrorGroup, glyphOpen) {
		t.Errorf("block-code mirror row used its own (expanded) glyph instead of the collapsed source's:\n%s", mirrorGroup)
	}
	if !strings.Contains(mirrorGroup, glyphCollapsed) {
		t.Errorf("block-code mirror row should show the source's collapsed glyph:\n%s", mirrorGroup)
	}
}
