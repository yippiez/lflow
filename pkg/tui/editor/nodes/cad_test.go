package nodes

import (
	"bytes"
	"encoding/binary"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// cadRows builds a subtree the way an outline holds one: a root row and its
// indented children, given in the source form the presets use.
func cadRows(src string) editor.NodeSubtree { return cadParseSource(src) }

// cadNode turns the same source into the fake NodeRef the plugin sees.
func cadNode(src string) *fakeNode {
	var build func(t editor.NodeSubtree, id string) *fakeNode
	build = func(t editor.NodeSubtree, id string) *fakeNode {
		n := &fakeNode{uuid: id, typ: database.TypeCAD, text: t.Text}
		for i, k := range t.Kids {
			c := build(k, id+string(rune('a'+i)))
			c.parent = n
			n.kids = append(n.kids, c)
		}
		return n
	}
	return build(cadParseSource(src), "cad")
}

const cadPlate = `difference
  box 2.4 1.4 0.4 r=0.12
  translate x=0.7
    cylinder r=0.4 h=1`

// ── the look: it must read with no geometry involved at all ────────────────

func TestCadHeadIsTheOnlyThingMarked(t *testing.T) {
	yellow := editor.NodeTheme().Yellow
	runes := []rune("difference r=0.2")
	got := cadHeadColor(runes)
	for i := 0; i < len("difference"); i++ {
		if got[i] != yellow {
			t.Fatalf("rune %d of the head = %q, want yellow", i, got[i])
		}
	}
	for i := len("difference"); i < len(runes); i++ {
		if c, ok := got[i]; ok {
			t.Errorf("rune %d (%q) is tinted %q — only the head is marked", i, string(runes[i]), c)
		}
	}
	// lexical: a word no vocabulary has still reads as the row's verb
	if c := cadHeadColor([]rune("frobnicate x=1"))[0]; c != yellow {
		t.Errorf("unknown head = %q, want yellow like any other", c)
	}
	if got := cadHeadColor([]rune("   ")); got != nil {
		t.Errorf("blank row = %v, want no tint", got)
	}
}

func TestCadTailPreviewsTheSubtree(t *testing.T) {
	tail := cadTail(cadRows("difference\n  box 2 2 2\n  sphere r=1"))
	for _, want := range []string{"box 2 2 2", "sphere r=1"} {
		if !strings.Contains(tail, want) {
			t.Errorf("tail = %q, want it to mention %q", tail, want)
		}
	}
	// a long model is cut rather than shoving the row off screen
	long := cadTail(cadRows(cadPlate))
	if !strings.Contains(long, "…") || len([]rune(long)) > 80 {
		t.Errorf("long tail = %q, want it ellipsized", long)
	}
	if !strings.HasPrefix(tail, editor.NodeTheme().Dim) {
		t.Errorf("tail = %q, want a dim prefix", tail)
	}
	// a leaf is already the whole call
	if tail := cadTail(cadRows("sphere r=1")); tail != "" {
		t.Errorf("leaf tail = %q, want empty", tail)
	}
	// and the same flattening is the structured context
	if got := cadFlatten(cadRows(cadPlate)); got !=
		"difference(box 2.4 1.4 0.4 r=0.12, translate x=0.7(cylinder r=0.4 h=1))" {
		t.Errorf("flatten = %q", got)
	}
}

// ── the language ──────────────────────────────────────────────────────────

func TestCadParseNamedAndPositional(t *testing.T) {
	c := cadParse("  Box 2 1 3 r=0.25 axis=X ")
	if c.head != "box" {
		t.Errorf("head = %q, want box", c.head)
	}
	// `box 2 1 3` is `box x=2 y=1 z=3`
	if c.num("x", 0, 9) != 2 || c.num("y", 1, 9) != 1 || c.num("z", 2, 9) != 3 {
		t.Errorf("positional args = %v", c.pos)
	}
	if c.num("r", -1, 9) != 0.25 {
		t.Errorf("r = %v, want 0.25", c.num("r", -1, 9))
	}
	if c.word("axis") != "x" {
		t.Errorf("axis = %q, want x", c.word("axis"))
	}
	// a named value wins over the slot, and a missing one falls to the default
	if got := cadParse("cylinder 5 h=2").num("h", 1, 9); got != 2 {
		t.Errorf("named h = %v, want 2", got)
	}
	if got := cadParse("sphere").num("r", 0, 1); got != 1 {
		t.Errorf("default r = %v, want 1", got)
	}
}

func TestCadCompileErrorsNameTheRow(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"frobnicate", `unknown shape or operator "frobnicate"`},
		{"union", "no children"},
		{"translate z=1", "nothing to work on"},
		{"preset teapot", `no preset "teapot"`},
		{"", "empty model"},
		{"mirror axis=q\n  sphere r=1", "axis must be"},
	} {
		if _, err := cadCompile(cadRows(tc.src), 0); err == nil ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("compile(%q) = %v, want an error containing %q", tc.src, err, tc.want)
		}
	}
	// a failing CHILD names its own row, once
	_, err := cadCompile(cadRows("union\n  frobnicate"), 0)
	if err == nil || !strings.HasPrefix(err.Error(), "frobnicate · ") {
		t.Errorf("child error = %v, want the offending row named", err)
	}
	if strings.Count(err.Error(), " · ") != 1 {
		t.Errorf("child error = %v, want the row named exactly once", err)
	}
}

// ── the geometry ──────────────────────────────────────────────────────────

// dist compiles a source and evaluates it at a point.
func dist(t *testing.T, src string, p vec) float64 {
	t.Helper()
	op, err := cadCompile(cadRows(src), 0)
	if err != nil {
		t.Fatalf("compile(%q): %v", src, err)
	}
	return op.dist(p)
}

func TestCadPrimitiveDistances(t *testing.T) {
	near := func(got, want float64, what string) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
	near(dist(t, "sphere r=1", vec{}), -1, "sphere center")
	near(dist(t, "sphere r=1", vec{2, 0, 0}), 1, "sphere outside")
	near(dist(t, "box 2 2 2", vec{}), -1, "box center")
	near(dist(t, "box 2 2 2", vec{2, 0, 0}), 1, "box face")
	near(dist(t, "cylinder r=1 h=2", vec{}), -1, "cylinder center")
	near(dist(t, "cylinder r=1 h=2", vec{0, 0, 2}), 1, "above the cap")
	near(dist(t, "torus r=2 t=0.5", vec{2, 0, 0}), -0.5, "torus tube")
	near(dist(t, "torus r=2 t=0.5", vec{}), 1.5, "torus hole")
	near(dist(t, "cone r=1 r2=0 h=2", vec{0, 0, -1}), 0, "cone base")
}

func TestCadBooleansAndModifiers(t *testing.T) {
	inside := func(src string, p vec) bool { return dist(t, src, p) < 0 }

	two := "%s\n  translate x=-0.5\n    sphere r=1\n  translate x=0.5\n    sphere r=1"
	un := strings_Sprintf(two, "union")
	in := strings_Sprintf(two, "intersect")
	df := strings_Sprintf(two, "difference")

	if !inside(un, vec{-1.2, 0, 0}) {
		t.Error("union must cover what only one operand covers")
	}
	if inside(in, vec{-1.2, 0, 0}) || !inside(in, vec{}) {
		t.Error("intersect must keep only the overlap")
	}
	if inside(df, vec{}) || !inside(df, vec{-1.2, 0, 0}) {
		t.Error("difference must remove the overlap and keep the rest")
	}
	// smooth fills the seam where a plain union is exactly on the surface
	tangent := "%s\n  translate x=-1\n    sphere r=1\n  translate x=1\n    sphere r=1"
	if dist(t, strings_Sprintf(tangent, "union"), vec{}) < 0 {
		t.Fatal("setup: touching spheres are not solid at the tangent point")
	}
	if dist(t, strings_Sprintf(tangent, "smooth k=0.5"), vec{}) >= 0 {
		t.Error("smooth must fill the seam between touching operands")
	}

	// modifiers
	if !inside("translate x=5\n  sphere r=1", vec{5, 0, 0}) {
		t.Error("translate must move the solid")
	}
	if !inside("scale f=3\n  sphere r=1", vec{2.9, 0, 0}) {
		t.Error("scale must grow the solid")
	}
	if !inside("rotate y=90\n  cylinder r=0.5 h=4", vec{1.5, 0, 0}) {
		t.Error("a Z cylinder turned 90° about Y must lie along X")
	}
	if !inside("mirror axis=x\n  translate x=1\n    sphere r=0.4", vec{-1, 0, 0}) {
		t.Error("mirror must reproduce the solid on the far side")
	}
	// shell hollows: the wall is solid, the middle is not
	if inside("shell t=0.2\n  sphere r=1", vec{}) || !inside("shell t=0.2\n  sphere r=1", vec{1, 0, 0}) {
		t.Error("shell must leave a wall around an empty middle")
	}
}

func TestCadBoundsFollowTheModel(t *testing.T) {
	op, err := cadCompile(cadRows(cadPlate), 0)
	if err != nil {
		t.Fatal(err)
	}
	lo, hi := op.bounds()
	size := hi.sub(lo)
	// a difference is bounded by what it cuts FROM, not by the cutter
	if math.Abs(size.x-2.4) > 1e-9 || math.Abs(size.y-1.4) > 1e-9 || math.Abs(size.z-0.4) > 1e-9 {
		t.Errorf("bounds = %v, want the plate's 2.4 × 1.4 × 0.4", size)
	}
	// a rotation transforms the box rather than keeping the old one
	op, _ = cadCompile(cadRows("rotate y=90\n  cylinder r=0.5 h=4"), 0)
	lo, hi = op.bounds()
	if math.Abs(hi.x-2) > 1e-6 || math.Abs(hi.z-0.5) > 1e-6 {
		t.Errorf("rotated bounds = %v..%v, want the cylinder lying along X", lo, hi)
	}
}

func TestCadEveryPresetCompilesAndMeshes(t *testing.T) {
	names := cadPresetNames()
	if len(names) < 6 {
		t.Fatalf("the library has only %d entries", len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			op, err := cadCompile(cadRows("preset "+name), 0)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			lo, hi := op.bounds()
			if s := hi.sub(lo); s.maxc() <= 0 || math.IsInf(s.maxc(), 0) {
				t.Fatalf("bounds are unusable: %v..%v", lo, hi)
			}
			mesh, err := cadPolygonize(op, 20)
			if err != nil {
				t.Fatalf("mesh: %v", err)
			}
			if len(mesh.tris) < 32 {
				t.Errorf("%s meshed to %d triangles — is it empty?", name, len(mesh.tris))
			}
		})
	}
	// the bare name is the same model as the explicit form
	a, _ := cadCompile(cadRows("preset washer"), 0)
	b, _ := cadCompile(cadRows("washer"), 0)
	for _, p := range []vec{{}, {0.75, 0, 0}, {2, 0, 0}} {
		if a.dist(p) != b.dist(p) {
			t.Errorf("at %v: `preset washer` = %v, `washer` = %v", p, a.dist(p), b.dist(p))
		}
	}
}

func TestCadMeshIsAccurateClosedAndOutwardFacing(t *testing.T) {
	op, _ := cadCompile(cadRows("sphere r=1"), 0)
	mesh, err := cadPolygonize(op, 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(mesh.tris) < 300 {
		t.Fatalf("only %d triangles for a sphere", len(mesh.tris))
	}
	worst := 0.0
	for _, v := range mesh.verts {
		worst = math.Max(worst, math.Abs(v.len()-1))
	}
	if worst > 0.05 {
		t.Errorf("worst vertex radius error = %.3f, want < 0.05", worst)
	}
	// winding: on a sphere every outward normal points away from the center
	for _, tri := range mesh.tris {
		a, b, c := mesh.verts[tri[0]], mesh.verts[tri[1]], mesh.verts[tri[2]]
		n := b.sub(a).cross(c.sub(a)).norm()
		if n.dot(a.add(b).add(c).mul(1.0/3)) < 0 {
			t.Fatalf("triangle %v is wound inward", tri)
		}
	}
	// closed: every directed edge appears once, and so does its opposite —
	// which is what makes the STL watertight enough to slice
	op, _ = cadCompile(cadRows(cadPlate), 0)
	mesh, err = cadPolygonize(op, 24)
	if err != nil {
		t.Fatal(err)
	}
	edges := map[[2]int]int{}
	for _, tri := range mesh.tris {
		for i := 0; i < 3; i++ {
			edges[[2]int{tri[i], tri[(i+1)%3]}]++
		}
	}
	for e, n := range edges {
		if n != 1 || edges[[2]int{e[1], e[0]}] != 1 {
			t.Fatalf("edge %v used %d times, opposite %d — the mesh is not closed",
				e, n, edges[[2]int{e[1], e[0]}])
		}
	}
}

func TestCadSTLIsBinarySTL(t *testing.T) {
	op, _ := cadCompile(cadRows("sphere r=1"), 0)
	mesh, err := cadPolygonize(op, 16)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := mesh.writeSTL(&buf, "sphere"); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.Len(), 84+50*len(mesh.tris); got != want {
		t.Errorf("stl size = %d, want %d", got, want)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("lflow cad")) {
		t.Errorf("stl header = %q", buf.Bytes()[:16])
	}
	var n uint32
	binary.Read(bytes.NewReader(buf.Bytes()[80:84]), binary.LittleEndian, &n)
	if int(n) != len(mesh.tris) {
		t.Errorf("stl says %d triangles, mesh has %d", n, len(mesh.tris))
	}
}

func TestCadRenderDrawsTheModel(t *testing.T) {
	op, _ := cadCompile(cadRows("sphere r=1"), 0)
	img, err := cadRender(op, 48, 32)
	if err != nil {
		t.Fatal(err)
	}
	lum := func(x, y int) int {
		r, g, b, _ := img.At(x, y).RGBA()
		return int(r>>8) + int(g>>8) + int(b>>8)
	}
	if lum(24, 16) <= lum(0, 0) {
		t.Error("the lit model should be brighter than the background")
	}
}

// ── the node ──────────────────────────────────────────────────────────────

func TestCadRunBuildsAnSTLAndReportsIt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	h := newFakeHost(t)
	n := cadNode(cadPlate)

	cmd := runCAD(h, n)
	if cmd == nil {
		t.Fatal("alt+r returned no command")
	}
	msg, ok := cmd().(cadBuiltMsg)
	if !ok || msg.err != nil {
		t.Fatalf("build message = %+v", msg)
	}
	msg.HandleNodePlugin(h)

	if len(h.runOut) != 3 || h.runErr {
		t.Fatalf("run band = %+v (err=%v), want the three-line summary", h.runOut, h.runErr)
	}
	if !strings.Contains(h.runOut[0], "triangles") || !strings.Contains(h.runOut[0], "2.40 × 1.40 × 0.40") {
		t.Errorf("first line = %q, want the count and the bounds", h.runOut[0])
	}
	path := strings.TrimPrefix(h.runOut[1], "→ ")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the stl was not written: %v", err)
	}
	if st.Size() < 84 {
		t.Errorf("stl is %d bytes", st.Size())
	}
	// the mesh is a CACHE, not notebook content
	if !strings.Contains(path, filepath.Join("lflow", "cad")) {
		t.Errorf("stl path = %q, want the lflow cad cache dir", path)
	}
	// the build also leaves the drawing the view shows
	if _, ok := h.NodeStore(n.UUID())["cadPic"].(image.Image); !ok {
		t.Error("the build should leave a picture behind")
	}
}

func TestCadRunReportsABadRowInTheBand(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	h := newFakeHost(t)
	n := cadNode("union\n  cylinder r=nope h=1\n  frobnicate")

	msg := runCAD(h, n)().(cadBuiltMsg)
	if msg.err == nil {
		t.Fatal("a bad row should fail the build")
	}
	msg.HandleNodePlugin(h)
	if !h.runErr || len(h.runOut) != 1 {
		t.Fatalf("run band = %+v (err=%v), want one error line", h.runOut, h.runErr)
	}
	if !strings.Contains(h.runOut[0], "frobnicate") {
		t.Errorf("error = %q, want the offending row named", h.runOut[0])
	}
}

func TestCadRunOnAnEmptyRowPrintsTheLanguage(t *testing.T) {
	h := newFakeHost(t)
	if cmd := runCAD(h, cadNode("")); cmd != nil {
		t.Error("an empty row should answer immediately, not build")
	}
	out := strings.Join(h.runOut, "\n")
	for _, want := range []string{"shapes", "booleans", "modifiers", "presets", "mug", "CHILD rows"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help band is missing %q", want)
		}
	}
	if len(h.runOut) > 6 {
		t.Errorf("the help is %d lines — it has to be readable in place", len(h.runOut))
	}
}

func TestCadDetailSetting(t *testing.T) {
	h := newFakeHost(t)
	for _, tc := range []struct {
		set  string
		want int
	}{{"coarse", 40}, {"", 64}, {"normal", 64}, {"fine", 112}} {
		h.settings = map[string]string{"cad.detail": tc.set}
		if got := cadDetail(h); got != tc.want {
			t.Errorf("cad.detail=%q → %d cells, want %d", tc.set, got, tc.want)
		}
	}
}

func TestCadViewDrawsAndCaches(t *testing.T) {
	h := newFakeHost(t)
	n := cadNode("sphere r=1")

	bands := cadView{}.Bands(h, n, "", 60, 0, 12, true)
	if len(bands) < 2 {
		t.Fatalf("bands = %d, want a header and picture rows", len(bands))
	}
	if !strings.Contains(bands[0], "esc close") {
		t.Errorf("header = %q, want the close hint", bands[0])
	}
	if !strings.Contains(bands[1], "▀") {
		t.Errorf("picture row = %q, want half-block cells", bands[1])
	}
	// the drawing is cached under the rows it came from …
	first, _ := h.NodeStore(n.UUID())["cadPic"].(image.Image)
	cadView{}.Bands(h, n, "", 60, 0, 12, true)
	if again, _ := h.NodeStore(n.UUID())["cadPic"].(image.Image); again != first {
		t.Error("a redraw should reuse the cached picture")
	}
	// … and an edit re-renders
	n.text = "sphere r=2"
	cadView{}.Bands(h, n, "", 60, 0, 12, true)
	if again, _ := h.NodeStore(n.UUID())["cadPic"].(image.Image); again == first {
		t.Error("editing the model should redraw it")
	}

	// a model that does not build says so instead of drawing nothing
	bands = cadView{}.Bands(h, cadNode("frobnicate"), "", 60, 0, 12, true)
	if len(bands) != 1 || !strings.Contains(bands[0], "unknown shape") {
		t.Errorf("broken model bands = %q, want the build error", bands)
	}
	// and an EMPTY row answers with the language — the question a blank cad row
	// is really asking — scrollable, so it never depends on the run band's tail
	empty := cadNode("")
	bands = cadView{}.Bands(h, empty, "", 80, 0, 20, true)
	joined := strings.Join(bands, "\n")
	for _, want := range []string{"shapes", "booleans", "modifiers", "presets", "mug"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the empty row's view is missing %q", want)
		}
	}
	if got := (cadView{}).Lines(h, empty, 80); got != len(bands) {
		t.Errorf("Lines = %d but Bands gave %d — the scroll would not clamp", got, len(bands))
	}
	// the view drives nothing: the arrows fall through to the central scroll
	if _, handled := (cadView{}).Key(h, n, tea.KeyMsg{Type: tea.KeyRight}); handled {
		t.Error("the view should not capture the arrows")
	}
}

func TestCadOpenPrefersA3DViewerThenAPicture(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// no viewer anywhere: the open falls through to a rendered picture, and the
	// host opener is what fails last (there is no desktop in a test)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("LFLOW_CAD_VIEWER", "")
	if _, ok := cadViewerBin(); ok {
		t.Error("no 3D viewer should be found on an empty PATH")
	}
	h := newFakeHost(t)
	msg := openCAD(h, cadNode("sphere r=1"))().(cadOpenedMsg)
	if !msg.png {
		t.Error("without a 3D viewer the open should fall back to a picture")
	}
	png, _ := cadCache("cad", "png")
	if st, err := os.Stat(png); err != nil || st.Size() == 0 {
		t.Errorf("the picture should have been drawn to %s: %v", png, err)
	}

	// $LFLOW_CAD_VIEWER wins and may carry arguments
	dir := t.TempDir()
	bin := filepath.Join(dir, "myviewer")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("LFLOW_CAD_VIEWER", "myviewer --fullscreen")
	got, ok := cadViewerBin()
	if !ok || filepath.Base(got[0]) != "myviewer" || got[1] != "--fullscreen" {
		t.Errorf("viewer = %v (%v), want the override with its flag", got, ok)
	}

	// an empty row has nothing to open
	h = newFakeHost(t)
	if cmd := openCAD(h, cadNode("")); cmd != nil || !strings.Contains(h.flash, "nothing to open") {
		t.Errorf("empty open = %v, flash %q", cmd, h.flash)
	}
}

func TestCadTypeIsRegistered(t *testing.T) {
	if !database.ValidTypes[database.TypeCAD] {
		t.Error("the cad type is not accepted by the CLI")
	}
}

// strings_Sprintf keeps the boolean table above readable without pulling fmt
// into the test's imports for one call.
func strings_Sprintf(tmpl, head string) string {
	return strings.Replace(tmpl, "%s", head, 1)
}
