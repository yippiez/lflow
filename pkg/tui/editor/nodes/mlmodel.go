package nodes

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The mlmodel node: an ML model built from PRIMITIVES, not presets. The
// mlmodel node is the model ROOT — its text is the model's name — and the
// architecture IS the subtree of mlop primitive nodes beneath it (mlop.go):
// siblings in order form a pipeline, a plain op's children continue the
// pipeline after it, and a combinator (repeat, residual, recur, branch,
// add/mul/concat, loss) consumes its children as its block. Non-mlop children
// (bullets, …) are comments — the compiler skips them.
//
// alt+e on the root opens the COMPILE CARD: every stage with its inferred
// output shape and weight count, the first shape error in red. alt+r flashes
// the total — ephemeral, like every run output. The reference architectures
// (RNN, Transformer, GAN, SAM, differentiable logic gate network) are NOT in
// the product: they live in mlmodel_test.go as the acceptance tests proving
// the primitive set spans them.
//
// Weight counts are ORDER-OF-MAGNITUDE (biases and minor terms ignored),
// there to sanity-check a definition, not to plan a training run.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeMLModel, Label: "ML Model",
		InlineEditable: true, // the node text is the model's name
		Glyph:          func() (string, string) { return "◈", editor.NodeTheme().Cyan },
		Render:         mlRender,
		Run:            runMLModel,
		View:           mlView{},
		ToContext:      mlToContext,
		OnRemove: func(h editor.NodeHost, uuid string) {
			delete(h.NodeStore(uuid), "mlmodel")
		},
	})
}

// ── the primitive expression ────────────────────────────────────────────────

// mlOp is one parsed primitive: the node text "attend heads=8 kv=encoder"
// reads as kind "attend", kv {heads:8, kv:encoder}; "conv 768 k16 s16" as
// kind "conv", pos [768, k16, s16].
type mlOp struct {
	kind string
	pos  []string
	kv   map[string]string
}

func mlParse(text string) mlOp {
	op := mlOp{kv: map[string]string{}}
	f := strings.Fields(text)
	if len(f) == 0 {
		return op
	}
	op.kind = strings.ToLower(f[0])
	for _, a := range f[1:] {
		if i := strings.IndexByte(a, '='); i > 0 {
			op.kv[strings.ToLower(a[:i])] = a[i+1:]
		} else {
			op.pos = append(op.pos, a)
		}
	}
	return op
}

// mlKinds is the primitive vocabulary — mlop.go colors an unknown kind red.
var mlKinds = map[string]bool{
	// sources
	"input": true, "noise": true, "state": true,
	// learned transforms
	"linear": true, "conv": true, "embed": true, "attend": true, "gates": true, "norm": true,
	// free glue
	"act": true, "softmax": true, "flatten": true, "tokens": true, "pool": true,
	// combinators
	"repeat": true, "residual": true, "recur": true, "branch": true,
	"add": true, "mul": true, "concat": true, "loss": true,
}

func (o mlOp) posInt(i int) (int64, error) {
	if i >= len(o.pos) {
		return 0, fmt.Errorf("%s needs a number in position %d", o.kind, i+1)
	}
	v, err := strconv.ParseInt(o.pos[i], 10, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%s: %q is not a positive number", o.kind, o.pos[i])
	}
	return v, nil
}

func (o mlOp) kvInt(k string, def int64) (int64, error) {
	s, ok := o.kv[k]
	if !ok {
		return def, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%s: %s=%q is not a positive number", o.kind, k, s)
	}
	return v, nil
}

// ── shapes ──────────────────────────────────────────────────────────────────

// mlDims is a tensor shape; nil = no stream yet (before a source op).
type mlDims []int64

func (d mlDims) String() string {
	if d == nil {
		return "·"
	}
	parts := make([]string, len(d))
	for i, v := range d {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(parts, "x")
}

func (d mlDims) eq(o mlDims) bool {
	if len(d) != len(o) {
		return false
	}
	for i := range d {
		if d[i] != o[i] {
			return false
		}
	}
	return true
}

func (d mlDims) last() int64 { return d[len(d)-1] }

func mlParseDims(s string) (mlDims, error) {
	var d mlDims
	for _, p := range strings.Split(strings.ToLower(s), "x") {
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("bad shape %q", s)
		}
		d = append(d, v)
	}
	return d, nil
}

// ── the compiler ────────────────────────────────────────────────────────────

// mlStage is one compiled line of the card: the primitive, its inferred
// output shape, its weight count — or the error that stopped the compile.
type mlStage struct {
	uuid  string
	depth int
	text  string
	out   mlDims
	param int64
	err   string
}

type mlEnv struct {
	branches map[string]mlDims
	inRecur  bool
	stateDim int64
	stages   []mlStage
	total    int64
}

// mlResult is a whole compile: the stage list, the weight total, and the
// first error ("" when the model is clean).
type mlResult struct {
	stages []mlStage
	total  int64
	err    string
}

func mlCompile(root editor.NodeRef) mlResult {
	env := &mlEnv{branches: map[string]mlDims{}}
	_, err := mlEvalSeq(env, mlOpChildren(root), nil, 0)
	r := mlResult{stages: env.stages, total: env.total}
	if err != nil {
		r.err = err.Error()
	}
	return r
}

// mlOpChildren filters a node's children to the mlop primitives — anything
// else (bullets, notes) is a comment the compiler skips.
func mlOpChildren(n editor.NodeRef) []editor.NodeRef {
	var out []editor.NodeRef
	for _, c := range n.Children() {
		if c.Type() == database.TypeMLOp {
			out = append(out, c)
		}
	}
	return out
}

func mlEvalSeq(env *mlEnv, nodes []editor.NodeRef, in mlDims, depth int) (mlDims, error) {
	cur := in
	for _, n := range nodes {
		var err error
		cur, err = mlEvalNode(env, n, cur, depth)
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}

func mlEvalNode(env *mlEnv, n editor.NodeRef, in mlDims, depth int) (mlDims, error) {
	op := mlParse(n.Text())
	si := len(env.stages)
	env.stages = append(env.stages, mlStage{uuid: n.UUID(), depth: depth, text: strings.TrimSpace(n.Text())})
	fail := func(err error) (mlDims, error) {
		env.stages[si].err = err.Error()
		return nil, err
	}
	done := func(out mlDims, param int64) {
		env.stages[si].out, env.stages[si].param = out, param
	}
	kids := mlOpChildren(n)

	switch op.kind {
	case "repeat":
		nrep, err := op.posInt(0)
		if err != nil {
			return fail(err)
		}
		if in == nil {
			return fail(fmt.Errorf("repeat needs an input stream"))
		}
		before := env.total
		out, err := mlEvalSeq(env, kids, in, depth+1)
		if err != nil {
			return nil, err // recorded on the failing child
		}
		if !out.eq(in) {
			return fail(fmt.Errorf("repeat block must preserve its shape (%s → %s)", in, out))
		}
		block := env.total - before
		env.total += block * (nrep - 1)
		done(out, block*nrep)
		return out, nil

	case "residual":
		if in == nil {
			return fail(fmt.Errorf("residual needs an input stream"))
		}
		before := env.total
		out, err := mlEvalSeq(env, kids, in, depth+1)
		if err != nil {
			return nil, err
		}
		if !out.eq(in) {
			return fail(fmt.Errorf("residual branch must match its input (%s → %s)", in, out))
		}
		done(in, env.total-before)
		return in, nil

	case "recur":
		if len(in) != 2 {
			return fail(fmt.Errorf("recur needs a sequence input (steps x features), got %s", in))
		}
		savedIn, savedDim := env.inRecur, env.stateDim
		env.inRecur, env.stateDim = true, 0
		before := env.total
		stepOut, err := mlEvalSeq(env, kids, mlDims{in[1]}, depth+1)
		carry := env.stateDim
		env.inRecur, env.stateDim = savedIn, savedDim
		if err != nil {
			return nil, err
		}
		if carry > 0 && !stepOut.eq(mlDims{carry}) {
			return fail(fmt.Errorf("recur block must end at the state size %d, got %s", carry, stepOut))
		}
		out := mlDims{in[0]}
		out = append(out, stepOut...)
		done(out, env.total-before)
		return out, nil

	case "branch":
		if len(op.pos) == 0 {
			return fail(fmt.Errorf("branch needs a name"))
		}
		name := op.pos[0]
		before := env.total
		bout, err := mlEvalSeq(env, kids, nil, depth+1)
		if err != nil {
			return nil, err
		}
		env.branches[name] = bout
		done(bout, env.total-before)
		return in, nil // a branch is a side definition — the stream passes by

	case "add", "mul", "concat":
		if len(kids) < 2 {
			return fail(fmt.Errorf("%s needs at least two child paths", op.kind))
		}
		if in == nil {
			return fail(fmt.Errorf("%s needs an input stream", op.kind))
		}
		before := env.total
		var outs []mlDims
		for _, k := range kids {
			o, err := mlEvalNode(env, k, in, depth+1)
			if err != nil {
				return nil, err
			}
			outs = append(outs, o)
		}
		out := append(mlDims{}, outs[0]...)
		for _, o := range outs[1:] {
			if op.kind == "concat" {
				if len(o) != len(out) || !o[:len(o)-1].eq(out[:len(out)-1]) {
					return fail(fmt.Errorf("concat paths disagree (%s vs %s)", outs[0], o))
				}
				out[len(out)-1] += o.last()
			} else if !o.eq(out) {
				return fail(fmt.Errorf("%s paths disagree (%s vs %s)", op.kind, outs[0], o))
			}
		}
		done(out, env.total-before)
		return out, nil

	case "loss":
		if len(op.pos) == 0 {
			return fail(fmt.Errorf("loss needs a kind (mse, xent, adversarial, …)"))
		}
		for _, name := range op.pos[1:] {
			if _, ok := env.branches[name]; !ok {
				return fail(fmt.Errorf("loss references unknown branch %q", name))
			}
		}
		done(in, 0)
		return in, nil

	default: // a plain op; its children continue the pipeline after it
		out, param, err := mlApply(env, op, in)
		if err != nil {
			return fail(err)
		}
		env.total += param
		done(out, param)
		if len(kids) > 0 {
			return mlEvalSeq(env, kids, out, depth+1)
		}
		return out, nil
	}
}

// mlApply evaluates one non-combinator primitive: (input shape) → (output
// shape, weight count).
func mlApply(env *mlEnv, op mlOp, in mlDims) (mlDims, int64, error) {
	need := func(want string) error {
		return fmt.Errorf("%s needs %s, got %s", op.kind, want, in)
	}
	switch op.kind {
	case "input":
		if in != nil {
			return nil, 0, fmt.Errorf("input must start a pipeline")
		}
		if len(op.pos) == 0 {
			return nil, 0, fmt.Errorf("input needs a shape, e.g. input 3x64x64")
		}
		d, err := mlParseDims(op.pos[0])
		return d, 0, err
	case "noise":
		if in != nil {
			return nil, 0, fmt.Errorf("noise must start a pipeline")
		}
		n, err := op.posInt(0)
		if err != nil {
			return nil, 0, err
		}
		return mlDims{n}, 0, nil
	case "state":
		if !env.inRecur {
			return nil, 0, fmt.Errorf("state only lives inside a recur block")
		}
		if len(in) != 1 {
			return nil, 0, need("a flat per-step stream")
		}
		s, err := op.posInt(0)
		if err != nil {
			return nil, 0, err
		}
		env.stateDim = s
		return mlDims{in[0] + s}, 0, nil
	case "linear":
		if in == nil {
			return nil, 0, need("an input stream")
		}
		o, err := op.posInt(0)
		if err != nil {
			return nil, 0, err
		}
		out := append(append(mlDims{}, in[:len(in)-1]...), o)
		return out, in.last() * o, nil
	case "conv":
		if len(in) != 3 {
			return nil, 0, need("a CxHxW image stream")
		}
		o, err := op.posInt(0)
		if err != nil {
			return nil, 0, err
		}
		k, s := int64(3), int64(1)
		for _, p := range op.pos[1:] {
			if v, err := strconv.ParseInt(strings.TrimPrefix(p, "k"), 10, 64); err == nil && strings.HasPrefix(p, "k") {
				k = v
			} else if v, err := strconv.ParseInt(strings.TrimPrefix(p, "s"), 10, 64); err == nil && strings.HasPrefix(p, "s") {
				s = v
			}
		}
		return mlDims{o, in[1] / s, in[2] / s}, k * k * in[0] * o, nil
	case "embed":
		if in == nil {
			return nil, 0, need("a token stream")
		}
		v, err := op.posInt(0)
		if err != nil {
			return nil, 0, err
		}
		d, err := op.posInt(1)
		if err != nil {
			return nil, 0, err
		}
		return append(append(mlDims{}, in...), d), v * d, nil
	case "norm":
		if in == nil {
			return nil, 0, need("an input stream")
		}
		return in, 2 * in.last(), nil
	case "act", "softmax":
		if in == nil {
			return nil, 0, need("an input stream")
		}
		return in, 0, nil
	case "flatten":
		if in == nil {
			return nil, 0, need("an input stream")
		}
		p := int64(1)
		for _, v := range in {
			p *= v
		}
		return mlDims{p}, 0, nil
	case "tokens":
		if len(in) != 3 {
			return nil, 0, need("a CxHxW image stream")
		}
		return mlDims{in[1] * in[2], in[0]}, 0, nil
	case "pool":
		if len(op.pos) == 0 {
			return nil, 0, fmt.Errorf("pool needs a mode (sum, mean, max)")
		}
		arg, err := op.posInt(1)
		if err != nil {
			return nil, 0, err
		}
		switch len(in) {
		case 3:
			return mlDims{in[0], in[1] / arg, in[2] / arg}, 0, nil // spatial window
		case 1:
			return mlDims{arg}, 0, nil // group readout (the DLGN sum)
		}
		return nil, 0, need("a flat or CxHxW stream")
	case "attend":
		if len(in) != 2 {
			return nil, 0, need("a sequence stream (tokens x dim)")
		}
		d := in.last()
		heads, err := op.kvInt("heads", 1)
		if err != nil {
			return nil, 0, err
		}
		if d%heads != 0 {
			return nil, 0, fmt.Errorf("attend: dim %d is not divisible by heads=%d", d, heads)
		}
		if name, ok := op.kv["kv"]; ok {
			kvd, ok := env.branches[name]
			if !ok {
				return nil, 0, fmt.Errorf("attend: unknown branch %q", name)
			}
			if len(kvd) != 2 || kvd.last() != d {
				return nil, 0, fmt.Errorf("attend: kv branch %q is %s, need tokens x %d", name, kvd, d)
			}
		}
		return in, 4 * d * d, nil // Q K V O
	case "gates":
		if len(in) != 1 {
			return nil, 0, need("a flat binary stream")
		}
		n, err := op.posInt(0)
		if err != nil {
			return nil, 0, err
		}
		set, err := op.kvInt("set", 16)
		if err != nil {
			return nil, 0, err
		}
		// one learned distribution over the gate set per gate; the 2-input
		// wiring onto the previous layer is random and fixed (weight-free)
		return mlDims{n}, n * set, nil
	case "":
		return nil, 0, fmt.Errorf("empty primitive")
	}
	return nil, 0, fmt.Errorf("unknown primitive %q", op.kind)
}

// mlHuman renders a parameter count the way model cards do: 641M, 1.3B, 768K.
func mlHuman(p int64) string {
	format := func(v float64, suffix string) string {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + suffix
	}
	switch {
	case p >= 1_000_000_000:
		return format(float64(p)/1e9, "B")
	case p >= 1_000_000:
		return format(float64(p)/1e6, "M")
	case p >= 1_000:
		return format(float64(p)/1e3, "K")
	}
	return strconv.FormatInt(p, 10)
}

// ── inline row, alt+r, agent context ────────────────────────────────────────

// mlRender is the root's inline body: the model name plus a dim chip carrying
// the compiled weight total — {model} while empty, a red {!} on a compile
// error (open alt+e for the failing stage).
func mlRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	chip := th.Dim + "{model}" + th.Reset
	if len(mlOpChildren(n)) > 0 {
		if r := mlCompile(n); r.err != "" {
			chip = th.Red + "{!}" + th.Reset
		} else {
			chip = th.Dim + "{≈" + mlHuman(r.total) + "}" + th.Reset
		}
	}
	return th.FG + n.Text() + th.Reset + " " + chip
}

// runMLModel (alt+r) flashes the compiled total — ephemeral, never persisted.
func runMLModel(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	r := mlCompile(n)
	switch {
	case len(r.stages) == 0:
		h.NodeFlash("empty model — add primitive lines beneath it (/type ML Op)")
	case r.err != "":
		h.NodeFlash("compile error: " + r.err)
	default:
		h.NodeFlash(fmt.Sprintf("≈ %s params · %d stages (order-of-magnitude)", mlHuman(r.total), len(r.stages)))
	}
	return nil
}

// mlToContext hands an agent the compiled header; the primitives themselves
// nest inside as their own elements (mlop.go), so the whole graph is legible.
func mlToContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	r := mlCompile(n)
	attrs := ""
	if r.err != "" {
		attrs = `error="` + strings.ReplaceAll(r.err, `"`, "&quot;") + `"`
	} else if len(r.stages) > 0 {
		attrs = `params="` + mlHuman(r.total) + `"`
	}
	return "mlmodel", attrs, ""
}

// ── the compile card (alt+e) ────────────────────────────────────────────────

// mlCard is the cached compile shown by the card (NodeStore, key "mlmodel").
type mlCard struct{ r mlResult }

func mlCardOf(h editor.NodeHost, uuid string) *mlCard {
	s := h.NodeStore(uuid)
	c, _ := s["mlmodel"].(*mlCard)
	if c == nil {
		c = &mlCard{}
		s["mlmodel"] = c
	}
	return c
}

// mlView is the read-only compile card: header, then one line per stage with
// its output shape and weight count; the failing stage in red.
type mlView struct{}

func (mlView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	mlCardOf(h, n.UUID()).r = mlCompile(n)
	return true
}

func (mlView) Leave(h editor.NodeHost, n editor.NodeRef) {}

func (mlView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 1 + len(mlCardOf(h, n.UUID()).r.stages)
}

func (mlView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	return nil, false // read-only; esc/scroll handled centrally
}

func (mlView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	r := mlCardOf(h, n.UUID()).r
	var lines []string
	switch {
	case len(r.stages) == 0:
		lines = append(lines, rail+th.Dim+"empty model — add primitive lines beneath it (/type ML Op)"+th.Reset)
	case r.err != "":
		lines = append(lines, rail+th.Red+"compile error: "+r.err+th.Reset)
	default:
		lines = append(lines, rail+th.Cyan+"≈ "+mlHuman(r.total)+" params"+th.Reset+th.Dim+" · "+
			strconv.Itoa(len(r.stages))+" stages · shapes and weights per stage"+th.Reset)
	}
	textW, shapeW := 0, 0
	for _, s := range r.stages {
		if w := 2*s.depth + len([]rune(s.text)); w > textW {
			textW = w
		}
		if w := len(s.out.String()); w > shapeW {
			shapeW = w
		}
	}
	for _, s := range r.stages {
		txt := strings.Repeat("  ", s.depth) + s.text
		if s.err != "" {
			lines = append(lines, rail+"  "+th.Red+txt+" ← "+s.err+th.Reset)
			continue
		}
		pad := textW - len([]rune(txt)) + 2
		if pad < 1 {
			pad = 1
		}
		params := ""
		if s.param > 0 {
			params = "  " + mlHuman(s.param)
		}
		shape := fmt.Sprintf("%-*s", shapeW, s.out.String())
		lines = append(lines, rail+"  "+th.FG+txt+th.Reset+strings.Repeat(" ", pad)+th.Dim+shape+params+th.Reset)
	}
	for i := range lines {
		lines[i] = editor.NodeClip(lines[i], width)
	}
	return editor.NodeWindowBands(lines, scroll, winH)
}
