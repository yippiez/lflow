package nodes

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The CAD node: a solid model composed AS an outline — the Math node's idea in
// three dimensions. A row is one CALL: a shape (`sphere r=1`), a boolean whose
// operands are its CHILD rows (`difference`), a modifier (`translate z=2`), or a
// preset (`preset mug`). The outline structure IS the CSG tree, so geometry is
// edited with the ordinary outline keys — indent a cylinder under a difference
// to drill it.
//
// The whole type is this one file, and it stays small by leaning on signed
// distance fields: one function answering "how far is the surface from here"
// gives CSG (booleans are min/max on two distances), a mesh (walk a grid, find
// the zero crossing) and a picture (sphere-trace a ray). No meshes to boolean,
// no vertex bookkeeping, no third-party geometry.
//
//	⌥r  build: mesh the model and write an STL — that is "the model"
//	⌥e  draw it inline, one fixed view, in half-block cells
//	⌥o  open it: a 3D viewer if the machine has one, else a rendered PNG in the
//	    host's image viewer (the image node's escape hatch)
//
// It wears the Math node's restraint: an ordinary bullet, ONE yellow head word,
// a dim preview of the subtree. Families are read from the word, not a palette.
//
// WARNING (invariant): the mesh is EPHEMERAL like every run output. The STL and
// the PNG are cache files keyed by node uuid, never blobs in the DB and never
// synced — the outline stores the model's SOURCE, and geometry rebuilds from it.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeCAD, Label: "CAD",
		InlineEditable: true,
		SpanColor:      cadHeadColor, // the one signal: the row's verb
		BodyTail:       cadTail,      // the dim subtree preview
		Run:            runCAD,       // ⌥r: mesh it to an STL
		View:           cadView{},    // ⌥e: draw it here
		OpenHost:       openCAD,      // ⌥o: 3D viewer, else a picture
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			return "cad", "", cadFlatten(editor.NodeSubtreeOf(n))
		},
	})
}

// ── the look: no geometry, so a model reads even when it cannot build ───────

// cadHeadColor marks the head word yellow — lexically, the first word whatever
// it says, so a half-typed row looks right and there is no vocabulary lookup in
// the render path.
func cadHeadColor(runes []rune) map[int]string {
	i := 0
	for i < len(runes) && runes[i] == ' ' {
		i++
	}
	j := i
	for j < len(runes) && runes[j] != ' ' {
		j++
	}
	if i == j {
		return nil
	}
	out := make(map[int]string, j-i)
	for k := i; k < j; k++ {
		out[k] = editor.NodeTheme().Yellow
	}
	return out
}

// cadFlatten renders a subtree as one line: a leaf is its row, a parent is
// `head(child, child)`. Pure text — it is the preview and the context body.
func cadFlatten(t editor.NodeSubtree) string {
	head := strings.TrimSpace(t.Text)
	if len(t.Kids) == 0 {
		return head
	}
	kids := make([]string, 0, len(t.Kids))
	for _, k := range t.Kids {
		if s := cadFlatten(k); s != "" {
			kids = append(kids, s)
		}
	}
	if head == "" {
		head = "union"
	}
	if len(kids) == 0 {
		return head
	}
	return head + "(" + strings.Join(kids, ", ") + ")"
}

// cadTail is the dim preview after a parent row, so a collapsed model still
// reads. A leaf is already the whole call and gets nothing.
func cadTail(t editor.NodeSubtree) string {
	if len(t.Kids) == 0 {
		return ""
	}
	s := strings.TrimPrefix(cadFlatten(t), strings.TrimSpace(t.Text))
	if r := []rune(s); len(r) > 56 {
		s = string(r[:55]) + "…"
	}
	th := editor.NodeTheme()
	return th.Dim + " " + s + th.Reset
}

// ── the language: one call per row ─────────────────────────────────────────

// cadCall is a parsed row: its head word and the arguments, named (`r=1`) or
// positional (`box 2 1 3` ≡ `box x=2 y=1 z=3`).
type cadCall struct {
	head  string
	named map[string]float64
	words map[string]string
	pos   []float64
}

func cadParse(line string) cadCall {
	c := cadCall{named: map[string]float64{}, words: map[string]string{}}
	for i, f := range strings.Fields(strings.TrimSpace(line)) {
		if i == 0 {
			c.head = strings.ToLower(f)
			continue
		}
		if k, v, ok := strings.Cut(f, "="); ok {
			k = strings.ToLower(k)
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				c.named[k] = n
			} else {
				c.words[k] = strings.ToLower(v)
			}
			continue
		}
		if n, err := strconv.ParseFloat(f, 64); err == nil {
			c.pos = append(c.pos, n)
		} else {
			c.words[""] = strings.ToLower(f) // a bare word: a preset name, an axis
		}
	}
	return c
}

// num reads a parameter: the named key first, then the positional slot, then the
// default. pos < 0 means "named only".
func (c cadCall) num(key string, pos int, def float64) float64 {
	if v, ok := c.named[key]; ok {
		return v
	}
	if pos >= 0 && pos < len(c.pos) {
		return c.pos[pos]
	}
	return def
}

func (c cadCall) word(key string) string {
	if v, ok := c.words[key]; ok {
		return v
	}
	return c.words[""]
}

// ── the model: a compiled tree of ops ──────────────────────────────────────

type vec struct{ x, y, z float64 }

func (a vec) add(b vec) vec     { return vec{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec) sub(b vec) vec     { return vec{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec) mul(s float64) vec { return vec{a.x * s, a.y * s, a.z * s} }
func (a vec) len() float64      { return math.Sqrt(a.x*a.x + a.y*a.y + a.z*a.z) }
func (a vec) max(b vec) vec     { return vec{math.Max(a.x, b.x), math.Max(a.y, b.y), math.Max(a.z, b.z)} }
func (a vec) min(b vec) vec     { return vec{math.Min(a.x, b.x), math.Min(a.y, b.y), math.Min(a.z, b.z)} }
func (a vec) maxc() float64     { return math.Max(a.x, math.Max(a.y, a.z)) }
func (a vec) norm() vec {
	l := a.len()
	if l == 0 {
		return vec{}
	}
	return a.mul(1 / l)
}
func (a vec) abs() vec          { return vec{math.Abs(a.x), math.Abs(a.y), math.Abs(a.z)} }
func (a vec) dot(b vec) float64 { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec) cross(b vec) vec {
	return vec{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}

// cadOp is one compiled row: its verb and the numbers it needs, resolved once so
// the distance function — called millions of times — never parses text.
type cadOp struct {
	kind   string
	v      vec      // box half-extents · translate offset · rotate angles (radians)
	r, r2  float64  // radii
	h, k   float64  // height / thickness · blend radius
	mirror [3]bool  //
	kids   []*cadOp //
}

// cadCompile turns an outline subtree into ops, expanding presets on the way. An
// empty row is a container: its children are unioned.
func cadCompile(t editor.NodeSubtree, depth int) (*cadOp, error) {
	if depth > 32 {
		return nil, fmt.Errorf("the model nests too deeply")
	}
	c := cadParse(t.Text)
	kids := make([]*cadOp, 0, len(t.Kids))
	for _, k := range t.Kids {
		op, err := cadCompile(k, depth+1)
		if err != nil {
			return nil, cadNameRow(k.Text, err)
		}
		kids = append(kids, op)
	}
	op := &cadOp{kind: c.head, kids: kids}

	switch c.head {
	case "": // a bare container
		if len(kids) == 0 {
			return nil, fmt.Errorf("empty model — write a shape: sphere r=1 · box 2 2 2 · preset mug")
		}
		op.kind = "union"

	case "sphere":
		op.r = c.num("r", 0, 1)

	case "box":
		x := c.num("x", 0, 1)
		op.v = vec{x, c.num("y", 1, x), c.num("z", 2, x)}.mul(0.5)
		op.r = c.num("r", -1, 0) // rounded edges

	case "cylinder":
		op.r, op.h = c.num("r", 0, 1), c.num("h", 1, 2)

	case "cone":
		op.r, op.r2, op.h = c.num("r", 0, 1), c.num("r2", 1, 0), c.num("h", 2, 2)

	case "torus":
		op.r, op.r2 = c.num("r", 0, 1), c.num("t", 1, 0.3)

	case "union", "difference", "intersect":
		if len(kids) == 0 {
			return nil, fmt.Errorf("%s has no children — indent the shapes it combines under it", c.head)
		}

	case "smooth":
		if len(kids) == 0 {
			return nil, fmt.Errorf("smooth has no children — indent the shapes it blends under it")
		}
		op.k = math.Max(c.num("k", 0, 0.2), 0)

	case "translate":
		op.v = vec{c.num("x", 0, 0), c.num("y", 1, 0), c.num("z", 2, 0)}

	case "rotate":
		d := math.Pi / 180
		op.v = vec{c.num("x", 0, 0) * d, c.num("y", 1, 0) * d, c.num("z", 2, 0) * d}

	case "scale":
		op.r = c.num("f", 0, 1)
		if op.r <= 0 {
			return nil, fmt.Errorf("scale needs a positive factor")
		}

	case "mirror":
		axes := c.word("axis")
		if axes == "" {
			axes = "x"
		}
		for _, a := range axes {
			switch a {
			case 'x':
				op.mirror[0] = true
			case 'y':
				op.mirror[1] = true
			case 'z':
				op.mirror[2] = true
			default:
				return nil, fmt.Errorf("mirror axis must be x, y or z (got %q)", axes)
			}
		}

	case "shell":
		op.h = c.num("t", 0, 0.1)

	case "preset":
		name := c.word("name")
		src, ok := cadPresets[name]
		if !ok {
			return nil, fmt.Errorf("no preset %q — try: %s", name, strings.Join(cadPresetNames(), " "))
		}
		return cadCompile(cadParseSource(src), depth+1)

	default:
		if src, ok := cadPresets[c.head]; ok { // a bare preset name
			return cadCompile(cadParseSource(src), depth+1)
		}
		return nil, fmt.Errorf("unknown shape or operator %q", c.head)
	}

	if cadIsModifier(op.kind) {
		if len(kids) == 0 {
			return nil, fmt.Errorf("%s has nothing to work on — indent a shape under it", op.kind)
		}
	}
	return op, nil
}

// cadRowErr names the row a build error came from, so the run band reads
// "cylinder r=x · unknown …" instead of a bare message. It wraps once — the
// deepest row is the one that matters.
type cadRowErr struct {
	row string
	err error
}

func (e cadRowErr) Error() string { return e.row + " · " + e.err.Error() }
func (e cadRowErr) Unwrap() error { return e.err }

func cadNameRow(row string, err error) error {
	if _, named := err.(cadRowErr); named {
		return err
	}
	return cadRowErr{row: strings.TrimSpace(row), err: err}
}

func cadIsModifier(kind string) bool {
	switch kind {
	case "translate", "rotate", "scale", "mirror", "shell":
		return true
	}
	return false
}

// one is a modifier's operand: a single child passes through, several are
// unioned first, so `translate z=1` over three shapes moves all three.
func (o *cadOp) one(p vec) float64 {
	if len(o.kids) == 1 {
		return o.kids[0].dist(p)
	}
	d := math.Inf(1)
	for _, k := range o.kids {
		d = math.Min(d, k.dist(p))
	}
	return d
}

// dist is the signed distance from p to the model's surface: negative inside,
// positive outside. Every verb is a few lines of arithmetic, which is the whole
// reason this type fits in one file.
func (o *cadOp) dist(p vec) float64 {
	switch o.kind {
	case "sphere":
		return p.len() - o.r

	case "box":
		q := p.abs().sub(o.v.sub(vec{o.r, o.r, o.r}))
		return q.max(vec{}).len() + math.Min(q.maxc(), 0) - o.r

	case "cylinder":
		return cap2(math.Hypot(p.x, p.y)-o.r, math.Abs(p.z)-o.h/2)

	case "cone":
		// a capped cone: interpolate the radius along z, then cap the ends
		t := (p.z + o.h/2) / o.h
		r := o.r + (o.r2-o.r)*math.Min(math.Max(t, 0), 1)
		return cap2(math.Hypot(p.x, p.y)-r, math.Abs(p.z)-o.h/2)

	case "torus":
		return math.Hypot(math.Hypot(p.x, p.y)-o.r, p.z) - o.r2

	case "union":
		d := math.Inf(1)
		for _, k := range o.kids {
			d = math.Min(d, k.dist(p))
		}
		return d

	case "intersect":
		d := math.Inf(-1)
		for _, k := range o.kids {
			d = math.Max(d, k.dist(p))
		}
		return d

	case "difference":
		d := o.kids[0].dist(p)
		for _, k := range o.kids[1:] {
			d = math.Max(d, -k.dist(p))
		}
		return d

	case "smooth":
		d := o.kids[0].dist(p)
		for _, k := range o.kids[1:] {
			d = smoothMin(d, k.dist(p), o.k)
		}
		return d

	case "translate":
		return o.one(p.sub(o.v))

	case "rotate":
		return o.one(rotate(p, o.v, true))

	case "scale":
		return o.one(p.mul(1/o.r)) * o.r

	case "mirror":
		if o.mirror[0] {
			p.x = math.Abs(p.x)
		}
		if o.mirror[1] {
			p.y = math.Abs(p.y)
		}
		if o.mirror[2] {
			p.z = math.Abs(p.z)
		}
		return o.one(p)

	case "shell":
		return math.Abs(o.one(p)) - o.h/2
	}
	return math.Inf(1)
}

// cap2 is the distance to a rectangle in the (radial, axial) plane — the shared
// body of the capped solids of revolution.
func cap2(a, b float64) float64 {
	return math.Min(math.Max(a, b), 0) + math.Hypot(math.Max(a, 0), math.Max(b, 0))
}

// smoothMin is the polynomial smooth minimum: a filleted union over a band of
// width k, identical to min outside it.
func smoothMin(a, b, k float64) float64 {
	if k <= 0 {
		return math.Min(a, b)
	}
	h := math.Min(math.Max(0.5+0.5*(b-a)/k, 0), 1)
	return b + (a-b)*h - k*h*(1-h)
}

// rotate turns p by Euler angles (X then Y then Z); inv applies the inverse,
// which is what a modifier does to the sample point.
func rotate(p, a vec, inv bool) vec {
	sx, cx := math.Sincos(a.x)
	sy, cy := math.Sincos(a.y)
	sz, cz := math.Sincos(a.z)
	if inv {
		p = vec{p.x*cz + p.y*sz, -p.x*sz + p.y*cz, p.z}
		p = vec{p.x*cy - p.z*sy, p.y, p.x*sy + p.z*cy}
		return vec{p.x, p.y*cx + p.z*sx, -p.y*sx + p.z*cx}
	}
	p = vec{p.x, p.y*cx - p.z*sx, p.y*sx + p.z*cx}
	p = vec{p.x*cy + p.z*sy, p.y, -p.x*sy + p.z*cy}
	return vec{p.x*cz - p.y*sz, p.x*sz + p.y*cz, p.z}
}

// bounds is a conservative box around the solid, composed the way the shape is,
// so the mesher samples a tight region and the camera frames the model.
func (o *cadOp) bounds() (lo, hi vec) {
	switch o.kind {
	case "sphere":
		return vec{-o.r, -o.r, -o.r}, vec{o.r, o.r, o.r}
	case "box":
		return o.v.mul(-1), o.v
	case "cylinder":
		return vec{-o.r, -o.r, -o.h / 2}, vec{o.r, o.r, o.h / 2}
	case "cone":
		r := math.Max(o.r, o.r2)
		return vec{-r, -r, -o.h / 2}, vec{r, r, o.h / 2}
	case "torus":
		e := o.r + o.r2
		return vec{-e, -e, -o.r2}, vec{e, e, o.r2}
	case "difference":
		return o.kids[0].bounds() // removing material never grows it
	case "intersect":
		lo, hi = o.kids[0].bounds()
		for _, k := range o.kids[1:] {
			klo, khi := k.bounds()
			lo, hi = lo.max(klo), hi.min(khi)
		}
		return lo, hi
	case "translate":
		lo, hi = o.kidsBounds()
		return lo.add(o.v), hi.add(o.v)
	case "scale":
		lo, hi = o.kidsBounds()
		return lo.mul(o.r), hi.mul(o.r)
	case "rotate":
		lo, hi = o.kidsBounds()
		var out vec
		first := true
		for _, c := range corners(lo, hi) {
			w := rotate(c, o.v, false)
			if first {
				lo, hi, first = w, w, false
				continue
			}
			lo, hi = lo.min(w), hi.max(w)
		}
		_ = out
		return lo, hi
	case "mirror":
		lo, hi = o.kidsBounds()
		fold := func(l, h float64, on bool) (float64, float64) {
			if !on {
				return l, h
			}
			e := math.Max(math.Abs(l), math.Abs(h))
			return -e, e
		}
		lo.x, hi.x = fold(lo.x, hi.x, o.mirror[0])
		lo.y, hi.y = fold(lo.y, hi.y, o.mirror[1])
		lo.z, hi.z = fold(lo.z, hi.z, o.mirror[2])
		return lo, hi
	case "shell":
		lo, hi = o.kidsBounds()
		e := vec{o.h / 2, o.h / 2, o.h / 2}
		return lo.sub(e), hi.add(e)
	case "smooth":
		lo, hi = o.kidsBounds()
		e := vec{o.k, o.k, o.k}
		return lo.sub(e), hi.add(e)
	}
	return o.kidsBounds() // union, and anything else that just covers its kids
}

func (o *cadOp) kidsBounds() (lo, hi vec) {
	if len(o.kids) == 0 {
		return vec{}, vec{}
	}
	lo, hi = o.kids[0].bounds()
	for _, k := range o.kids[1:] {
		klo, khi := k.bounds()
		lo, hi = lo.min(klo), hi.max(khi)
	}
	return lo, hi
}

func corners(lo, hi vec) [8]vec {
	return [8]vec{
		{lo.x, lo.y, lo.z}, {hi.x, lo.y, lo.z}, {lo.x, hi.y, lo.z}, {hi.x, hi.y, lo.z},
		{lo.x, lo.y, hi.z}, {hi.x, lo.y, hi.z}, {lo.x, hi.y, hi.z}, {hi.x, hi.y, hi.z},
	}
}

// normal estimates the surface normal by central differences.
func (o *cadOp) normal(p vec, e float64) vec {
	return vec{
		o.dist(vec{p.x + e, p.y, p.z}) - o.dist(vec{p.x - e, p.y, p.z}),
		o.dist(vec{p.x, p.y + e, p.z}) - o.dist(vec{p.x, p.y - e, p.z}),
		o.dist(vec{p.x, p.y, p.z + e}) - o.dist(vec{p.x, p.y, p.z - e}),
	}.norm()
}

// ── the preset library, written in the node language ───────────────────────

// cadPresets are ready-made objects. Each is the SAME rows a user would type, so
// a preset is a worked example rather than a black box.
var cadPresets = map[string]string{
	"washer": `difference
  cylinder r=1 h=0.2
  cylinder r=0.5 h=0.4`,

	"pipe": `difference
  cylinder r=1 h=3
  cylinder r=0.8 h=3.2`,

	"mug": `union
  difference
    shell t=0.12
      cylinder r=1 h=2.4
    translate z=1.4
      cylinder r=1.2 h=0.8
  translate x=1
    rotate x=90
      torus r=0.55 t=0.13`,

	"vase": `difference
  shell t=0.1
    smooth k=0.5
      translate z=-0.8
        sphere r=1
      translate z=0.9
        cylinder r=0.45 h=1.4
  translate z=1.9
    cylinder r=1.2 h=0.8`,

	"table": `union
  translate z=1
    box 3 2 0.15 r=0.03
  mirror axis=xy
    translate x=1.3 y=0.8
      box 0.18 0.18 2 r=0.03`,

	"dumbbell": `smooth k=0.12
  translate x=-1.2
    sphere r=0.6
  translate x=1.2
    sphere r=0.6
  rotate y=90
    cylinder r=0.18 h=2.4`,

	"rocket": `union
  cylinder r=0.6 h=3
  translate z=2.1
    cone r=0.6 r2=0 h=1.2
  mirror axis=xy
    translate x=0.6 z=-1.2
      box 0.9 0.12 1 r=0.05`,

	"dice": `difference
  box 2 2 2 r=0.3
  translate z=1
    sphere r=0.24
  mirror axis=xy
    translate x=0.5 y=0.5 z=-1
      sphere r=0.24
  translate x=1
    sphere r=0.24
  translate x=1 y=0.5 z=0.5
    sphere r=0.24
  translate x=1 y=-0.5 z=-0.5
    sphere r=0.24
  mirror axis=yz
    translate x=-1 y=0.5 z=0.5
      sphere r=0.24`,
}

func cadPresetNames() []string {
	out := make([]string, 0, len(cadPresets))
	for n := range cadPresets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// cadParseSource turns an indented source (a preset) into the same subtree shape
// an outline produces — two spaces per level. It builds through pointers and
// converts at the end: appending to a parent's Kids reallocates that slice, so a
// pointer into it cannot be held while the tree is still growing.
func cadParseSource(src string) editor.NodeSubtree {
	type srcNode struct {
		text string
		kids []*srcNode
	}
	root := &srcNode{}
	stack := []*srcNode{root}
	for _, raw := range strings.Split(src, "\n") {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		depth := (len(raw)-len(strings.TrimLeft(raw, " ")))/2 + 1
		if depth > len(stack) {
			depth = len(stack)
		}
		n := &srcNode{text: text}
		parent := stack[depth-1]
		parent.kids = append(parent.kids, n)
		stack = append(stack[:depth], n)
	}

	var conv func(*srcNode) editor.NodeSubtree
	conv = func(n *srcNode) editor.NodeSubtree {
		out := editor.NodeSubtree{Text: n.text}
		for _, k := range n.kids {
			out.Kids = append(out.Kids, conv(k))
		}
		return out
	}
	if len(root.kids) == 1 {
		return conv(root.kids[0])
	}
	return conv(root)
}

// ── meshing: the grid walk that turns the field into triangles ─────────────

// cadMesh is an indexed triangle mesh.
type cadMesh struct {
	verts []vec
	tris  [][3]int
}

// cadPolygonize meshes the model with naive SURFACE NETS: one vertex inside each
// cell the surface crosses (the average of its edge crossings, nudged onto the
// surface), then a quad around every sign-changing grid edge. It is chosen over
// marching cubes for size: no 256-case table, and watertight by construction.
func cadPolygonize(o *cadOp, res int) (*cadMesh, error) {
	lo, hi := o.bounds()
	size := hi.sub(lo)
	longest := size.maxc()
	if longest <= 0 || math.IsNaN(longest) || math.IsInf(longest, 0) {
		return nil, fmt.Errorf("the model is empty")
	}
	cell := longest / float64(res)
	lo, hi = lo.sub(vec{2 * cell, 2 * cell, 2 * cell}), hi.add(vec{2 * cell, 2 * cell, 2 * cell})
	size = hi.sub(lo)

	nx := int(math.Ceil(size.x/cell)) + 1
	ny := int(math.Ceil(size.y/cell)) + 1
	nz := int(math.Ceil(size.z/cell)) + 1
	if (nx+1)*(ny+1)*(nz+1) > 6<<20 {
		return nil, fmt.Errorf("grid too fine for this model — lower the detail setting")
	}

	at := func(i, j, k int) float64 {
		return o.dist(vec{lo.x + float64(i)*cell, lo.y + float64(j)*cell, lo.z + float64(k)*cell})
	}
	field := make([]float64, (nx+1)*(ny+1)*(nz+1))
	idx := func(i, j, k int) int { return (k*(ny+1)+j)*(nx+1) + i }
	for k := 0; k <= nz; k++ {
		for j := 0; j <= ny; j++ {
			for i := 0; i <= nx; i++ {
				field[idx(i, j, k)] = at(i, j, k)
			}
		}
	}

	m := &cadMesh{}
	cellVert := make([]int32, nx*ny*nz)
	for i := range cellVert {
		cellVert[i] = -1
	}
	ci := func(i, j, k int) int { return (k*ny+j)*nx + i }

	for k := 0; k < nz; k++ {
		for j := 0; j < ny; j++ {
			for i := 0; i < nx; i++ {
				var sum vec
				n := 0
				for _, e := range cadCellEdges {
					a, b := e[0], e[1]
					va := field[idx(i+a[0], j+a[1], k+a[2])]
					vb := field[idx(i+b[0], j+b[1], k+b[2])]
					if (va < 0) == (vb < 0) {
						continue
					}
					t := va / (va - vb)
					pa := vec{lo.x + float64(i+a[0])*cell, lo.y + float64(j+a[1])*cell, lo.z + float64(k+a[2])*cell}
					pb := vec{lo.x + float64(i+b[0])*cell, lo.y + float64(j+b[1])*cell, lo.z + float64(k+b[2])*cell}
					sum = sum.add(pa.add(pb.sub(pa).mul(t)))
					n++
				}
				if n == 0 {
					continue
				}
				v := sum.mul(1 / float64(n))
				// one Newton step onto the true surface, bounded by half a cell:
				// what turns blocky averages into a smooth skin
				if d := o.dist(v); d != 0 && !math.IsNaN(d) {
					step := math.Min(math.Max(-d, -cell/2), cell/2)
					v = v.add(o.normal(v, cell*0.25).mul(step))
				}
				cellVert[ci(i, j, k)] = int32(len(m.verts))
				m.verts = append(m.verts, v)
			}
		}
	}

	quad := func(a, b, c, d int32, flip bool) {
		if a < 0 || b < 0 || c < 0 || d < 0 {
			return // an edge on the grid border: no full ring of cells
		}
		if flip {
			a, b, c, d = d, c, b, a
		}
		m.tris = append(m.tris, [3]int{int(a), int(b), int(c)}, [3]int{int(a), int(c), int(d)})
	}
	for k := 0; k <= nz; k++ {
		for j := 0; j <= ny; j++ {
			for i := 0; i <= nx; i++ {
				v0 := field[idx(i, j, k)]
				if i < nx && j > 0 && k > 0 && (v0 < 0) != (field[idx(i+1, j, k)] < 0) {
					quad(cellVert[ci(i, j-1, k-1)], cellVert[ci(i, j, k-1)],
						cellVert[ci(i, j, k)], cellVert[ci(i, j-1, k)], v0 >= 0)
				}
				if j < ny && i > 0 && k > 0 && (v0 < 0) != (field[idx(i, j+1, k)] < 0) {
					quad(cellVert[ci(i-1, j, k-1)], cellVert[ci(i-1, j, k)],
						cellVert[ci(i, j, k)], cellVert[ci(i, j, k-1)], v0 >= 0)
				}
				if k < nz && i > 0 && j > 0 && (v0 < 0) != (field[idx(i, j, k+1)] < 0) {
					quad(cellVert[ci(i-1, j-1, k)], cellVert[ci(i, j-1, k)],
						cellVert[ci(i, j, k)], cellVert[ci(i-1, j, k)], v0 >= 0)
				}
			}
		}
	}
	return m, nil
}

// cadCellEdges are a cell's 12 edges as pairs of corner offsets.
var cadCellEdges = [12][2][3]int{
	{{0, 0, 0}, {1, 0, 0}}, {{0, 1, 0}, {1, 1, 0}}, {{0, 0, 1}, {1, 0, 1}}, {{0, 1, 1}, {1, 1, 1}},
	{{0, 0, 0}, {0, 1, 0}}, {{1, 0, 0}, {1, 1, 0}}, {{0, 0, 1}, {0, 1, 1}}, {{1, 0, 1}, {1, 1, 1}},
	{{0, 0, 0}, {0, 0, 1}}, {{1, 0, 0}, {1, 0, 1}}, {{0, 1, 0}, {0, 1, 1}}, {{1, 1, 0}, {1, 1, 1}},
}

// writeSTL writes the mesh as binary STL — what every 3D viewer and slicer reads.
func (m *cadMesh) writeSTL(w io.Writer, name string) error {
	bw := bufio.NewWriterSize(w, 1<<16)
	var header [80]byte
	copy(header[:], "lflow cad · "+name)
	if _, err := bw.Write(header[:]); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, uint32(len(m.tris))); err != nil {
		return err
	}
	buf := make([]float32, 12)
	for _, t := range m.tris {
		a, b, c := m.verts[t[0]], m.verts[t[1]], m.verts[t[2]]
		for i, p := range [4]vec{b.sub(a).cross(c.sub(a)).norm(), a, b, c} {
			buf[i*3], buf[i*3+1], buf[i*3+2] = float32(p.x), float32(p.y), float32(p.z)
		}
		if err := binary.Write(bw, binary.LittleEndian, buf); err != nil {
			return err
		}
		if err := binary.Write(bw, binary.LittleEndian, uint16(0)); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// ── drawing: sphere-tracing the field ──────────────────────────────────────

// The one camera the node uses — a three-quarter view from above, the angle a
// CAD package shows a fresh part at. The inline drawing and the opened picture
// share it, so what opens is what the outline showed.
const cadYaw, cadPitch = -0.9, 0.55

// cadRender sphere-traces the model: from a point, the distance IS a safe step,
// so a ray reaches the surface in a handful of evaluations.
func cadRender(o *cadOp, w, h int) (*image.RGBA, error) {
	lo, hi := o.bounds()
	center := lo.add(hi).mul(0.5)
	radius := hi.sub(lo).mul(0.5).len()
	if radius <= 0 || math.IsNaN(radius) || math.IsInf(radius, 0) {
		return nil, fmt.Errorf("the model is empty")
	}
	const fov = 0.62
	dist := radius / math.Sin(fov/2)
	eye := center.add(vec{
		math.Cos(cadPitch) * math.Cos(cadYaw),
		math.Cos(cadPitch) * math.Sin(cadYaw),
		math.Sin(cadPitch),
	}.mul(dist))
	fwd := center.sub(eye).norm()
	right := fwd.cross(vec{0, 0, 1}).norm()
	up := right.cross(fwd).norm()
	light := right.mul(-0.55).add(up.mul(0.6)).add(fwd.mul(-0.6)).norm()

	eps, far := radius*0.001, dist+3*radius
	tanHalf, aspect := math.Tan(fov/2), float64(w)/float64(h)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		py := (1 - 2*(float64(y)+0.5)/float64(h)) * tanHalf
		for x := 0; x < w; x++ {
			px := (2*(float64(x)+0.5)/float64(w) - 1) * tanHalf * aspect
			ray := fwd.add(right.mul(px)).add(up.mul(py)).norm()

			t, hit := 0.0, false
			for i := 0; i < 128 && t < far; i++ {
				d := o.dist(eye.add(ray.mul(t)))
				if d < eps {
					hit = true
					break
				}
				t += d
			}
			if !hit {
				g := 0.06 + 0.05*float64(h-y)/float64(h)
				img.Set(x, y, color.RGBA{gamma(g), gamma(g * 1.05), gamma(g * 1.2), 255})
				continue
			}
			p := eye.add(ray.mul(t))
			n := o.normal(p, eps*2)
			diff := math.Max(n.dot(light), 0)
			amb := 0.22 + 0.18*(0.5+0.5*n.z) // sky above, bounce below
			rim := math.Pow(1-math.Max(-n.dot(ray), 0), 3)
			spec := math.Pow(math.Max(n.dot(light.sub(ray).norm()), 0), 48)
			lit := 0.62*(amb+0.85*diff) + 0.25*rim + 0.35*spec
			img.Set(x, y, color.RGBA{gamma(lit * 0.98), gamma(lit), gamma(lit * 1.06), 255})
		}
	}
	return img, nil
}

func gamma(v float64) uint8 {
	v = math.Min(math.Max(v, 0), 1)
	return uint8(math.Pow(v, 1/2.2)*255 + 0.5)
}

// ── the node: build, draw, open ────────────────────────────────────────────

// cadDetail maps the cad.detail setting to a mesh resolution — cells across the
// model's longest axis.
func cadDetail(h editor.NodeHost) int {
	switch h.NodeSetting("cad.detail") {
	case "coarse":
		return 40
	case "fine":
		return 112
	}
	return 64
}

// cadCache is where a node's model is materialized: one file per node and
// extension. A CACHE — the rows are the source of truth and rebuild it.
func cadCache(uuid, ext string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "lflow", "cad")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, uuid+"."+ext), nil
}

// cadBuiltMsg carries a finished build back into the plugin.
type cadBuiltMsg struct {
	uuid  string
	lines []string
	err   error
	pic   image.Image
}

func (msg cadBuiltMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	delete(h.NodeStore(msg.uuid), "cadBusy")
	if msg.err != nil {
		h.NodeSetRunOut(msg.uuid, []string{"cad · " + msg.err.Error()}, true)
		h.NodeFlash("cad · " + msg.err.Error())
		return nil
	}
	h.NodeStore(msg.uuid)["cadPic"] = msg.pic
	h.NodeSetRunOut(msg.uuid, msg.lines, false)
	h.NodeFlash(msg.lines[0])
	return nil
}

// runCAD (⌥r) builds the model: it meshes the rows to an STL — the thing ⌥o
// opens — and draws the picture ⌥e shows. An empty row answers with the
// language instead, which is where the shapes and the presets announce
// themselves.
func runCAD(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	t := editor.NodeSubtreeOf(n)
	uuid := n.UUID()
	if strings.TrimSpace(cadFlatten(t)) == "" {
		h.NodeSetRunOut(uuid, cadHelp(), false)
		h.NodeFlash("cad · the whole language → output")
		return nil
	}
	if busy, _ := h.NodeStore(uuid)["cadBusy"].(bool); busy {
		return nil
	}
	h.NodeStore(uuid)["cadBusy"] = true
	h.NodeFlash("cad · building…")
	res := cadDetail(h)

	return func() tea.Msg {
		start := time.Now()
		op, err := cadCompile(t, 0)
		if err != nil {
			return cadBuiltMsg{uuid: uuid, err: err}
		}
		mesh, err := cadPolygonize(op, res)
		if err != nil {
			return cadBuiltMsg{uuid: uuid, err: err}
		}
		path, err := cadCache(uuid, "stl")
		if err != nil {
			return cadBuiltMsg{uuid: uuid, err: err}
		}
		f, err := os.Create(path)
		if err != nil {
			return cadBuiltMsg{uuid: uuid, err: err}
		}
		if err := mesh.writeSTL(f, uuid); err != nil {
			f.Close()
			return cadBuiltMsg{uuid: uuid, err: err}
		}
		f.Close()

		pic, _ := cadRender(op, 320, 240) // the inline drawing; nil is survivable
		lo, hi := op.bounds()
		size := hi.sub(lo)
		return cadBuiltMsg{uuid: uuid, pic: pic, lines: []string{
			fmt.Sprintf("model · %d triangles · %.2f × %.2f × %.2f · %s",
				len(mesh.tris), size.x, size.y, size.z, time.Since(start).Round(time.Millisecond)),
			"→ " + cadShortPath(path),
			"⌥o opens it in a 3D viewer · ⌥e draws it here",
		}}
	}
}

// cadShortPath shortens a cache path for the run band.
func cadShortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// cadHelp is the whole language in a few lines — what an empty row answers ⌥r
// with, in the ordinary run band.
func cadHelp() []string {
	return []string{
		"shapes     sphere box cylinder cone torus",
		"booleans   union difference intersect smooth",
		"modifiers  translate rotate scale mirror shell",
		"presets    " + strings.Join(cadPresetNames(), " "),
		"a row is one call — sphere r=1 · box 2 2 2 · preset mug",
		"an operator's operands are its CHILD rows",
	}
}

// ── ⌥e: the picture, drawn in half-blocks ──────────────────────────────────

const cadViewRows = 14

// cadView draws the model inline. It renders on demand and caches by the rows it
// drew, so a redraw costs nothing and an edit re-renders once.
type cadView struct{}

func (cadView) Enter(h editor.NodeHost, n editor.NodeRef) bool { return true }
func (cadView) Leave(h editor.NodeHost, n editor.NodeRef)      {}

func (cadView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	if _, err := cadPicture(h, n); err != nil {
		return len(cadViewWords(err))
	}
	return 1 + cadViewRows
}

// cadViewWords is what the view shows when there is no model to draw: the whole
// language on an empty row — which is the question a blank CAD row asks — and
// the build error on a broken one. Scrollable like any band, so nothing here
// depends on the run band's last few lines.
func cadViewWords(err error) []string {
	if strings.Contains(err.Error(), "empty model") {
		return append([]string{"cad · nothing here yet — write one of these:"}, cadHelp()...)
	}
	return []string{"cad · " + err.Error()}
}

// Key: enter (and alt+o, which the focused view would otherwise swallow) opens
// the model; everything else falls through to the central scroll/defocus.
func (cadView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "enter", "alt+o":
		return openCAD(h, n), true
	}
	return nil, false
}

func (cadView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	rows := cadViewRows
	if winH-1 < rows {
		rows = winH - 1
	}
	if rows < 1 {
		rows = 1
	}
	cols := rows * 3 // a half-block cell is one pixel wide and two tall: 3:2
	if avail := width - editor.NodeVisibleWidth(rail) - 3; cols > avail {
		cols = avail
	}
	if cols < 8 {
		cols = 8
	}

	img, err := cadPicture(h, n)
	if err != nil {
		words := cadViewWords(err)
		color := th.Dim
		if len(words) == 1 {
			color = th.Red // a build error, not an invitation
		}
		out := make([]string, 0, len(words))
		for _, l := range words {
			out = append(out, editor.NodeClip(rail+th.Reset+color+"  "+l+th.Reset, width))
		}
		return editor.NodeWindowBands(out, scroll, winH)
	}
	content := []string{editor.NodeClip(rail+th.Reset+th.Dim+
		"  cad · enter opens it · ⌥r builds the mesh · esc close"+th.Reset, width)}
	for _, l := range editor.NodeHalfBlocks(img, cols, rows) {
		content = append(content, editor.NodeClip(rail+th.Reset+"  "+l, width))
	}
	return editor.NodeWindowBands(content, scroll, winH)
}

// cadPicture returns the drawing for the node's current rows, rendering it once
// and caching it under the rows it came from.
func cadPicture(h editor.NodeHost, n editor.NodeRef) (image.Image, error) {
	key := cadFlatten(editor.NodeSubtreeOf(n))
	d := h.NodeStore(n.UUID())
	if d["cadPicKey"] == key {
		if img, ok := d["cadPic"].(image.Image); ok {
			return img, nil
		}
	}
	op, err := cadCompile(editor.NodeSubtreeOf(n), 0)
	if err != nil {
		return nil, err
	}
	img, err := cadRender(op, 320, 240)
	if err != nil {
		return nil, err
	}
	d["cadPic"], d["cadPicKey"] = img, key
	return img, nil
}

// ── ⌥o: leave the terminal ─────────────────────────────────────────────────

// cadViewers are the 3D viewers to look for, in order. $LFLOW_CAD_VIEWER
// overrides the list.
var cadViewers = []string{"f3d", "fstl", "meshlab", "view3dscene",
	"prusa-slicer", "orca-slicer", "cura", "freecad"}

// cadOpenedMsg flashes the outcome of an open.
type cadOpenedMsg struct {
	via string
	png bool
	err error
}

func (msg cadOpenedMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	switch {
	case msg.err != nil:
		h.NodeFlash("cad · " + msg.err.Error())
	case msg.png:
		h.NodeFlash("cad · no 3D viewer — drew it and opened the picture in " + msg.via)
	default:
		h.NodeFlash("cad · opened in " + msg.via)
	}
	return nil
}

// openCAD (⌥o) hands the model to the desktop: a real 3D viewer when the machine
// has one — it gets the STL and can turn, measure and slice it — otherwise a
// full-size picture in the host's image viewer, which every desktop can show.
// A terminal cell cannot carry a solid; this is the way out.
func openCAD(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	t := editor.NodeSubtreeOf(n)
	if strings.TrimSpace(cadFlatten(t)) == "" {
		h.NodeFlash("cad · nothing to open — write a shape, or ⌥r for the language")
		return nil
	}
	uuid, res := n.UUID(), cadDetail(h)
	h.NodeFlash("cad · opening…")

	return func() tea.Msg {
		op, err := cadCompile(t, 0)
		if err != nil {
			return cadOpenedMsg{err: err}
		}
		if bin, ok := cadViewerBin(); ok {
			path, err := cadWriteSTL(op, uuid, res)
			if err != nil {
				return cadOpenedMsg{err: err}
			}
			cmd := exec.Command(bin[0], append(bin[1:], path)...)
			cmd.Stdout, cmd.Stderr = nil, nil
			if err := cmd.Start(); err != nil {
				return cadOpenedMsg{via: filepath.Base(bin[0]), err: err}
			}
			go func() { _ = cmd.Wait() }() // reap it; the editor does not care when
			return cadOpenedMsg{via: filepath.Base(bin[0])}
		}
		// no 3D viewer here: draw the model full size and open the picture
		img, err := cadRender(op, 1200, 900)
		if err != nil {
			return cadOpenedMsg{png: true, err: err}
		}
		path, err := cadCache(uuid, "png")
		if err != nil {
			return cadOpenedMsg{png: true, err: err}
		}
		f, err := os.Create(path)
		if err != nil {
			return cadOpenedMsg{png: true, err: err}
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			return cadOpenedMsg{png: true, err: err}
		}
		f.Close()
		via, err := editor.NodeOpenInHost(path)
		return cadOpenedMsg{via: via, png: true, err: err}
	}
}

// cadViewerBin resolves the 3D viewer to use, honoring $LFLOW_CAD_VIEWER (which
// may carry arguments). ok=false means this machine has none.
func cadViewerBin() ([]string, bool) {
	if custom := strings.Fields(strings.TrimSpace(os.Getenv("LFLOW_CAD_VIEWER"))); len(custom) > 0 {
		if bin, err := exec.LookPath(custom[0]); err == nil {
			return append([]string{bin}, custom[1:]...), true
		}
		return nil, false
	}
	for _, name := range cadViewers {
		if bin, err := exec.LookPath(name); err == nil {
			return []string{bin}, true
		}
	}
	return nil, false
}

// cadWriteSTL meshes the model and writes it where the viewer can find it.
func cadWriteSTL(op *cadOp, uuid string, res int) (string, error) {
	mesh, err := cadPolygonize(op, res)
	if err != nil {
		return "", err
	}
	path, err := cadCache(uuid, "stl")
	if err != nil {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := mesh.writeSTL(f, uuid); err != nil {
		f.Close()
		return "", err
	}
	return path, f.Close()
}
