package editor

import (
	"strings"
	"testing"
)

// mltree builds a Model node subtree from indented text, the way the same model
// reads in the outline — two spaces per level. It is the corpus's notation too.
func mltree(s string) *item {
	root := &item{name: "root", typ: "ml"}
	stack := []*item{root}
	for _, line := range strings.Split(strings.Trim(s, "\n"), "\n") {
		text := strings.TrimLeft(line, " ")
		if text == "" {
			continue
		}
		depth := (len(line) - len(text)) / 2
		if depth > len(stack)-1 {
			depth = len(stack) - 1 // clamp a jump deeper than one level
		}
		parent := stack[depth]
		it := &item{name: text, typ: "ml", parent: parent}
		parent.children = append(parent.children, it)
		stack = append(stack[:depth+1], it)
	}
	if len(root.children) == 1 {
		root.children[0].parent = nil
		return root.children[0]
	}
	return root
}

func mlleaf(name string) *item { return &item{name: name, typ: "ml"} }

func TestMLLeafPreview(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Linear 784 256", "Linear(784→256)"},
		{"Linear(784, 256)", "Linear(784→256)"},
		{"Linear 784→256", "Linear(784→256)"},
		{"fc 512 10", "Linear(512→10)"},
		{"Conv2d 3 64 k=3 p=1", "Conv2d(3→64, k=3, p=1)"},
		{"ReLU", "ReLU"},
		{"LayerNorm 768", "LayerNorm(768)"},
		{"attn 768 heads=12", "Attention(768, 12h)"},
		{"Dropout 0.1", "Dropout(0.1)"},
		{"Embedding 50257 768", "Embedding(50257→768)"},
		{"Encoder", "Encoder"}, // prose passes through untouched
	}
	for _, c := range cases {
		if got := mlLeaf(mlleaf(c.in)); got != c.want {
			t.Errorf("mlLeaf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMLParams(t *testing.T) {
	cases := []struct {
		name string
		it   *item
		want int
	}{
		{"linear with bias", mlleaf("Linear 784 256"), 784*256 + 256},
		{"linear without bias", mlleaf("Linear 784 256 bias=false"), 784 * 256},
		{"conv2d", mlleaf("Conv2d 3 64 k=3"), 3*64*9 + 64},
		{"grouped conv halves", mlleaf("Conv2d 64 64 k=3 g=2"), 32*64*9 + 64},
		{"layernorm", mlleaf("LayerNorm 768"), 1536},
		{"attention", mlleaf("Attention 768 12"), 4*768*768 + 4*768},
		{"feedforward multiplier", mlleaf("FeedForward 768 mult=4"), 2*768*3072 + 3072 + 768},
		{"feedforward explicit width", mlleaf("FeedForward 768 3072"), 2*768*3072 + 3072 + 768},
		{"activations are free", mlleaf("GELU"), 0},
		{"prose is free", mlleaf("Encoder"), 0},
		{"sum over children", mltree("sequential\n  Linear 4 8\n  Linear 8 2"), (4*8 + 8) + (8*2 + 2)},
		{"repeat multiplies the body", mltree("3×\n  Linear 4 8"), 3 * (4*8 + 8)},
		{"nested repeat multiplies once per level", mltree("2×\n  3×\n    Linear 4 8"), 6 * (4*8 + 8)},
	}
	for _, c := range cases {
		if got := mlParams(c.it); got != c.want {
			t.Errorf("%s: mlParams = %d, want %d", c.name, got, c.want)
		}
	}
}

// GPT-2 small is the reference the whole parameter path is calibrated against:
// 124M with weight tying, which is the number the model card quotes.
func TestMLParamsGPT2Small(t *testing.T) {
	n := mlParams(mltree(gpt2Small))
	if n < 120_000_000 || n > 128_000_000 {
		t.Fatalf("GPT-2 small = %d params (%s), want ~124M", n, mlFmtParams(n))
	}
}

func TestMLFmtParams(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{{0, ""}, {512, "512"}, {1536, "1.5K"}, {200_960, "201K"}, {124_400_000, "124.4M"}, {175_000_000_000, "175B"}}
	for _, c := range cases {
		if got := mlFmtParams(c.in); got != c.want {
			t.Errorf("mlFmtParams(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMLPreview(t *testing.T) {
	cases := []struct {
		name string
		it   *item
		want string
	}{
		{"sequence", mltree("sequential\n  Linear 784 256\n  ReLU"), "Linear(784→256) → ReLU"},
		{"named group drops its own name", mltree("Encoder\n  LayerNorm 768\n  GELU"), "LayerNorm(768) → GELU"},
		{"residual keeps its shape", mltree("residual\n  Linear 8 8\n  ReLU"), "x + (Linear(8→8) → ReLU)"},
		{"repeat drops its count", mltree("12×\n  Attention 768 12"), "Attention(768, 12h)"},
		{"nested repeat keeps its count", mltree("Model\n  12×\n    GELU"), "12× (GELU)"},
		{"nested group keeps its name", mltree("Model\n  Encoder\n    GELU\n    ReLU"), "Encoder(GELU → ReLU)"},
		{"concat branches", mltree("concat\n  Conv2d 3 8 k=1\n  Conv2d 3 8 k=3"), "[Conv2d(3→8, k=1) | Conv2d(3→8, k=3)]"},
		{"add branches", mltree("add\n  Identity\n  ReLU"), "Identity + ReLU"},
		{"leaf previews itself", mlleaf("ReLU"), "ReLU"},
	}
	for _, c := range cases {
		if got := mlPreview(c.it); got != c.want {
			t.Errorf("%s: mlPreview = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMLPreviewClips(t *testing.T) {
	deep := mltree("sequential\n" + strings.Repeat("  Linear 1024 1024\n", 20))
	got := mlPreview(deep)
	if r := []rune(got); len(r) > mlPreviewMax {
		t.Fatalf("preview is %d runes, want ≤ %d: %q", len(r), mlPreviewMax, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a clipped preview should end in an ellipsis: %q", got)
	}
}

func TestMLBodyTail(t *testing.T) {
	// a leaf shows only its size — its own text is already the whole layer.
	if got := mlBodyTail(mlleaf("Linear 784 256")); got != cDim+"201K"+cReset {
		t.Errorf("leaf tail = %q", got)
	}
	if got := mlBodyTail(mlleaf("ReLU")); got != "" {
		t.Errorf("a parameter-free leaf should have no tail, got %q", got)
	}
	// a container shows the shape of its subtree, then the size.
	got := mlBodyTail(mltree("sequential\n  Linear 4 8\n  ReLU"))
	if !strings.Contains(got, "Linear(4→8) → ReLU · 40") {
		t.Errorf("container tail = %q", got)
	}
}

func TestMLSpanColor(t *testing.T) {
	// the block name lights up by family, its arguments dim behind it.
	runes := []rune("Linear 784 256")
	got := mlSpanColor(mlleaf(string(runes)), runes)
	for i := 0; i < 6; i++ {
		if got[i] != mlColor(mlWeighted) {
			t.Fatalf("rune %d of the block name is %q, want the weighted tint", i, got[i])
		}
	}
	if got[7] != cDim {
		t.Errorf("arguments should be dim, got %q", got[7])
	}
	// families are distinct: a container reads as an operator, like math's.
	for _, c := range []struct {
		text string
		want string
	}{
		{"ReLU", mlColor(mlAct)},
		{"LayerNorm 768", mlColor(mlNorm)},
		{"Flatten", mlColor(mlShape)},
		{"residual", mlColor(mlContainerK)},
		{"12×", mlColor(mlContainerK)},
	} {
		r := []rune(c.text)
		if got := mlSpanColor(mlleaf(c.text), r); got[0] != c.want {
			t.Errorf("%q tinted %q, want %q", c.text, got[0], c.want)
		}
	}
	// prose is left alone entirely — a group label is not a block.
	if got := mlSpanColor(mlleaf("Encoder stack"), []rune("Encoder stack")); got != nil {
		t.Errorf("prose should not be tinted, got %v", got)
	}
}

func TestMLToTorchSequential(t *testing.T) {
	got := mlToTorch(mltree("MNIST MLP\n  Flatten\n  Linear 784 256\n  ReLU\n  Linear 256 10"))
	want := `import torch
import torch.nn as nn

# MNIST MLP · 203.5K params
model = nn.Sequential(  # MNIST MLP
    nn.Flatten(),
    nn.Linear(784, 256),
    nn.ReLU(),
    nn.Linear(256, 10),
)`
	if got != want {
		t.Fatalf("torch export mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMLToTorchResidualAndRepeat(t *testing.T) {
	got := mlToTorch(mltree("2×\n  residual\n    LayerNorm 8\n    Linear 8 8"))
	for _, want := range []string{
		"class Residual(nn.Module):",
		"        return x + self.body(x)",
		"model = nn.Sequential(*[Residual(",
		"for _ in range(2)]",
		"nn.LayerNorm(8),",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("torch export is missing %q:\n%s", want, got)
		}
	}
	// helper classes are emitted only when they are used.
	if strings.Contains(got, "class Concat") || strings.Contains(got, "class Add") {
		t.Errorf("unused helper classes were emitted:\n%s", got)
	}
}

func TestMLToTorchBranches(t *testing.T) {
	got := mlToTorch(mltree("concat\n  Conv2d 3 8 k=1\n  Conv2d 3 8 k=3 p=1"))
	if !strings.Contains(got, "class Concat(nn.Module):") || !strings.Contains(got, "model = Concat(") {
		t.Errorf("concat export:\n%s", got)
	}
	if !strings.Contains(got, "nn.Conv2d(3, 8, kernel_size=3, padding=1),") {
		t.Errorf("conv arguments did not survive the export:\n%s", got)
	}
	if got := mlToTorch(mltree("add\n  Identity\n  ReLU")); !strings.Contains(got, "class Add(nn.Module):") {
		t.Errorf("add export:\n%s", got)
	}
}

// A row PyTorch has no module for still exports something runnable, carrying its
// text — a half-written model never silently loses a line.
func TestMLToTorchKeepsUnknownRows(t *testing.T) {
	got := mlToTorch(mltree("sequential\n  Linear 4 4\n  quantize to int4\n  Permute 0 2 1"))
	for _, want := range []string{"nn.Identity(),  # quantize to int4", "nn.Identity(),  # Permute 0 2 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestMLToContext(t *testing.T) {
	c := mlToContext(mltree("Block\n  Linear 4 8\n  ReLU"))
	if c.tag != "model" {
		t.Errorf("tag = %q, want model", c.tag)
	}
	if c.body != "Linear(4→8) → ReLU · 40 params" {
		t.Errorf("body = %q", c.body)
	}
}

func TestMLContainerOf(t *testing.T) {
	cases := []struct {
		in   string
		want mlContainer
		n    int
		ok   bool
	}{
		{"sequential", mlSeq, 0, true},
		{"residual", mlResidual, 0, true},
		{"skip", mlResidual, 0, true},
		{"12×", mlRepeat, 12, true},
		{"×6", mlRepeat, 6, true},
		{"repeat 24", mlRepeat, 24, true},
		{"concat", mlParallel, 0, true},
		{"add", mlAdd, 0, true},
		{"Encoder", 0, 0, false},
		{"Linear 4 8", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		got, n, ok := mlContainerOf(c.in)
		if ok != c.ok || (ok && (got != c.want || n != c.n)) {
			t.Errorf("mlContainerOf(%q) = (%v, %d, %v), want (%v, %d, %v)", c.in, got, n, ok, c.want, c.n, c.ok)
		}
	}
}

// The type is inline-editable and hangs off one registry entry, like Math.
func TestMLRegistryEntry(t *testing.T) {
	nt := typeOf("ml")
	if nt.key != "ml" || nt.label != "Model" {
		t.Fatalf("the ml type is not registered: %+v", nt)
	}
	if !nt.inlineEditable || nt.spanColor == nil || nt.bodyTail == nil || nt.run == nil || nt.toContext == nil {
		t.Errorf("the ml entry is missing a hook: %+v", nt)
	}
}
