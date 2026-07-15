package nodes

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The reference architectures are the primitive set's ACCEPTANCE TESTS: each
// one (RNN, Transformer, GAN, SAM, differentiable logic gate network) must be
// buildable from mlop primitives alone, compile cleanly, and land on the
// closed-form weight count computed by hand in the comments.

var mlSeq int

// opN builds one mlop node with its children as the subtree beneath it.
func opN(text string, kids ...*fakeNode) *fakeNode {
	mlSeq++
	n := &fakeNode{uuid: fmt.Sprintf("op%d", mlSeq), typ: database.TypeMLOp, text: text, kids: kids}
	for _, k := range kids {
		k.parent = n
	}
	return n
}

// modelN builds the mlmodel root over a pipeline of primitives.
func modelN(name string, kids ...*fakeNode) *fakeNode {
	mlSeq++
	n := &fakeNode{uuid: fmt.Sprintf("model%d", mlSeq), typ: database.TypeMLModel, text: name, kids: kids}
	for _, k := range kids {
		k.parent = n
	}
	return n
}

func compileOK(t *testing.T, root *fakeNode) mlResult {
	t.Helper()
	r := mlCompile(root)
	if r.err != "" {
		t.Fatalf("%s must compile, got: %s", root.text, r.err)
	}
	return r
}

func TestMLParse(t *testing.T) {
	op := mlParse("Attend heads=8 kv=image-encoder")
	if op.kind != "attend" || op.kv["heads"] != "8" || op.kv["kv"] != "image-encoder" {
		t.Fatalf("parse = %+v", op)
	}
	op = mlParse("conv 768 k16 s16")
	if op.kind != "conv" || len(op.pos) != 3 || op.pos[0] != "768" {
		t.Fatalf("parse = %+v", op)
	}
}

// TestMLTransformer: embed 32000·512 (16,384,000) + 6 blocks of
// [norm 1024 + attend 4·512² (1,048,576)] + [norm 1024 + 512·2048 + 2048·512
// (2,097,152)] = 6·3,147,776 (18,886,656) + head 512·32000 (16,384,000)
// = 51,654,656 → 51.7M.
func TestMLTransformer(t *testing.T) {
	root := modelN("my-gpt",
		opN("input 1024"),
		opN("embed 32000 512"),
		opN("repeat 6",
			opN("residual",
				opN("norm"),
				opN("attend heads=8")),
			opN("residual",
				opN("norm"),
				opN("linear 2048"),
				opN("act gelu"),
				opN("linear 512"))),
		opN("linear 32000"),
		opN("softmax"),
	)
	r := compileOK(t, root)
	if got := mlHuman(r.total); got != "51.7M" {
		t.Fatalf("transformer params = %s (%d), want 51.7M", got, r.total)
	}
	last := r.stages[len(r.stages)-1]
	if !last.out.eq(mlDims{1024, 32000}) {
		t.Fatalf("transformer out = %s", last.out)
	}
}

// TestMLRNN: a vanilla recurrent tagger. Per step: concat the 256-wide carry
// onto the 64-wide input (state), linear 320→256 (81,920), tanh; head
// 256→10 (2,560). Total 84,480 → 84.5K; recur emits the hidden sequence.
func TestMLRNN(t *testing.T) {
	root := modelN("sequence tagger",
		opN("input 128x64"),
		opN("recur",
			opN("state 256"),
			opN("linear 256"),
			opN("act tanh")),
		opN("linear 10"),
		opN("softmax"),
	)
	r := compileOK(t, root)
	if got := mlHuman(r.total); got != "84.5K" {
		t.Fatalf("rnn params = %s (%d), want 84.5K", got, r.total)
	}
	for _, s := range r.stages {
		if s.text == "recur" && !s.out.eq(mlDims{128, 256}) {
			t.Fatalf("recur out = %s, want 128x256", s.out)
		}
	}
}

// TestMLGAN: two adversarial branches and no main stream. Generator
// noise 100 → 256 → 256 → 4096 (25,600 + 65,536 + 1,048,576 = 1,139,712);
// discriminator 3x64x64 → flatten 12288 → 256 → 1 (3,145,728 + 256 =
// 3,145,984). Total 4,285,696 → 4.3M; loss validates the branch names.
func TestMLGAN(t *testing.T) {
	root := modelN("faces gan",
		opN("branch generator",
			opN("noise 100"),
			opN("linear 256"),
			opN("act relu"),
			opN("linear 256"),
			opN("act relu"),
			opN("linear 4096")),
		opN("branch discriminator",
			opN("input 3x64x64"),
			opN("flatten"),
			opN("linear 256"),
			opN("act relu"),
			opN("linear 1"),
			opN("act sigmoid")),
		opN("loss adversarial generator discriminator"),
	)
	r := compileOK(t, root)
	if got := mlHuman(r.total); got != "4.3M" {
		t.Fatalf("gan params = %s (%d), want 4.3M", got, r.total)
	}

	bad := modelN("bad gan", opN("loss adversarial nosuch"))
	if r := mlCompile(bad); !strings.Contains(r.err, `unknown branch "nosuch"`) {
		t.Fatalf("loss must validate branches, got %q", r.err)
	}
}

// TestMLSAM: the image encoder is a ViT (patchify conv 16·16·3·768 = 589,824;
// 2 blocks of [norm 1536 + attend 4·768² = 2,360,832] + [norm 1536 + 768·3072
// ·2 = 4,720,128] = 14,161,920; neck 768·256 = 196,608) feeding a mask
// decoder that alternates self-attention with CROSS-attention onto the
// branch (2 blocks · 2·(4·256²) = 1,048,576; head 65,536). Total 16,062,464
// → 16.1M. Without the neck the cross-attend dims disagree — the compiler
// must say so.
func TestMLSAM(t *testing.T) {
	encoder := func(neck bool) *fakeNode {
		kids := []*fakeNode{
			opN("input 3x1024x1024"),
			opN("conv 768 k16 s16"),
			opN("tokens"),
			opN("repeat 2",
				opN("residual", opN("norm"), opN("attend heads=8")),
				opN("residual", opN("norm"), opN("linear 3072"), opN("act gelu"), opN("linear 768"))),
		}
		if neck {
			kids = append(kids, opN("linear 256"))
		}
		return opN("branch image-encoder", kids...)
	}
	decoder := []*fakeNode{
		opN("input 8x256"),
		opN("repeat 2",
			opN("residual", opN("attend heads=8")),
			opN("residual", opN("attend heads=8 kv=image-encoder"))),
		opN("linear 256"),
	}

	root := modelN("street segmenter", append([]*fakeNode{encoder(true)}, decoder...)...)
	r := compileOK(t, root)
	if got := mlHuman(r.total); got != "16.1M" {
		t.Fatalf("sam params = %s (%d), want 16.1M", got, r.total)
	}

	// no neck: the encoder ends at 768 while the decoder attends at 256
	bad := modelN("neckless", append([]*fakeNode{encoder(false)}, decoder...)...)
	if r := mlCompile(bad); !strings.Contains(r.err, "kv branch") {
		t.Fatalf("cross-attend must catch the dim mismatch, got %q", r.err)
	}
}

// TestMLDLGN: a differentiable logic gate network — one learned distribution
// over the 16 binary gates per gate, random fixed wiring. 6 layers of 64,000
// gates = 6·64,000·16 = 6,144,000 → 6.1M; group-sum readout to 10 classes.
func TestMLDLGN(t *testing.T) {
	root := modelN("parity logic net",
		opN("input 784"),
		opN("gates 64000"),
		opN("repeat 5",
			opN("gates 64000")),
		opN("pool sum 10"),
	)
	r := compileOK(t, root)
	if got := mlHuman(r.total); got != "6.1M" {
		t.Fatalf("dlgn params = %s (%d), want 6.1M", got, r.total)
	}
	last := r.stages[len(r.stages)-1]
	if !last.out.eq(mlDims{10}) {
		t.Fatalf("dlgn out = %s, want 10", last.out)
	}
}

// TestMLMerge: add/mul/concat take their children as parallel paths from the
// same input — concat sums the last dim, add/mul require agreement.
func TestMLMerge(t *testing.T) {
	root := modelN("paths",
		opN("input 32"),
		opN("concat",
			opN("linear 16"),
			opN("linear 8")),
	)
	r := compileOK(t, root)
	last := r.stages[len(r.stages)-1]
	if !last.out.eq(mlDims{8}) { // stages are depth-first: last is the 8-path
		t.Fatalf("last stage out = %s", last.out)
	}
	for _, s := range r.stages {
		if s.text == "concat" && !s.out.eq(mlDims{24}) {
			t.Fatalf("concat out = %s, want 24", s.out)
		}
	}

	bad := modelN("bad add", opN("input 32"), opN("add", opN("linear 16"), opN("linear 8")))
	if r := mlCompile(bad); !strings.Contains(r.err, "paths disagree") {
		t.Fatalf("add must require agreement, got %q", r.err)
	}
}

// TestMLErrors: the compiler explains itself — ops before a source, shape-
// breaking repeat blocks, and typos all name the problem.
func TestMLErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		root *fakeNode
		want string
	}{
		{"no input", modelN("m", opN("linear 10")), "needs an input"},
		{"repeat mismatch", modelN("m", opN("input 64"), opN("repeat 2", opN("linear 128"))), "preserve its shape"},
		{"unknown op", modelN("m", opN("input 64"), opN("linzear 10")), `unknown primitive "linzear"`},
		{"state outside recur", modelN("m", opN("input 64"), opN("state 32")), "inside a recur"},
		{"heads divide", modelN("m", opN("input 8x100"), opN("attend heads=8")), "not divisible"},
	} {
		if r := mlCompile(tc.root); !strings.Contains(r.err, tc.want) {
			t.Fatalf("%s: err = %q, want it to contain %q", tc.name, r.err, tc.want)
		}
	}
}

// TestMLOpRunFlash: alt+r on a primitive flashes its stage's shape + weights;
// the root flashes the total. Both ephemeral.
func TestMLOpRunFlash(t *testing.T) {
	h := newFakeHost(t)
	embed := opN("embed 32000 512")
	root := modelN("my-gpt", opN("input 1024"), embed)

	runMLOp(h, embed)
	if !strings.Contains(h.flash, "1024x512") || !strings.Contains(h.flash, "16.4M") {
		t.Fatalf("op flash = %q", h.flash)
	}
	runMLModel(h, root)
	if !strings.Contains(h.flash, "16.4M params") {
		t.Fatalf("model flash = %q", h.flash)
	}
	runMLOp(h, opN("linear 4"))
	if !strings.Contains(h.flash, "not inside") {
		t.Fatalf("orphan op flash = %q", h.flash)
	}
}

// TestMLRenderChips: the root chip carries the live total (red {!} on a
// compile error); an mlop keyword typo renders red.
func TestMLRenderChips(t *testing.T) {
	h := newFakeHost(t)
	root := modelN("my-gpt", opN("input 1024"), opN("embed 32000 512"))
	if got := mlRender(h, root); !strings.Contains(got, "{≈16.4M}") {
		t.Fatalf("root chip = %q", got)
	}
	broken := modelN("broken", opN("linear 10"))
	if got := mlRender(h, broken); !strings.Contains(got, "{!}") {
		t.Fatalf("broken chip = %q", got)
	}
	if got := moRender(h, opN("linzear 10")); !strings.Contains(got, "linzear") {
		t.Fatalf("typo render = %q", got)
	}
}
