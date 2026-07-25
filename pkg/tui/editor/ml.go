package editor

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The Model node (database.TypeML) is a neural network architecture composed AS
// an outline — the Math node's shape applied to deep learning. There is no
// parser and no separate editor: a node's text is either a BUILDING BLOCK with
// its arguments (Linear 784 256, Conv2d 3 64 k=3 p=1, ReLU, LayerNorm 512) or a
// CONTAINER (sequential, residual, 12×, concat, add) whose parts are its
// children. The outline structure IS the module graph.
//
// The type declares itself with one visual signal: the block name lights up by
// family (containers yellow, weighted layers cyan, activations purple, norms
// blue, shape ops green) with its arguments dimmed, while the node stays fully
// inline-editable — type "ReLU" and it lights up. A single layer lives on one
// row; a model fans out into a child tree, and every container row carries a dim
// linear PREVIEW of its subtree plus its parameter count (mlBodyTail), so the
// gestalt and the size of a model read at a glance without expanding.
//
// alt+r exports the node's subtree as runnable PyTorch (mlToTorch) into its
// ephemeral run band — run it on any node to get the module for just that part
// of the architecture.
//
// This file is the whole type: the registry hooks, the one block table that
// drives coloring AND code export AND parameter counting, and the pure
// preview / torch / param serializers.

// ── the block table: one source for coloring, torch, and parameters ─────────

// mlKind classifies a building block. It decides the block name's color and
// nothing else — composition is decided by the container the block sits in.
type mlKind int

const (
	mlWeighted  mlKind = iota // carries parameters: Linear, Conv2d, Attention, Embedding
	mlAct                     // pointwise nonlinearity: ReLU, GELU, Softmax
	mlNorm                    // normalization / regularization: LayerNorm, Dropout
	mlShape                   // moves data without learning: Flatten, MaxPool2d, Upsample
	mlContainerK              // composes children: sequential, residual, 12×, concat
)

// mlColor is the family tint, the ML answer to math's yellow operator glyph.
// Containers borrow math's operator yellow — they are the operators here.
func mlColor(k mlKind) string {
	switch k {
	case mlContainerK:
		return cYellow
	case mlWeighted:
		return cCyan
	case mlAct:
		return cMagenta
	case mlNorm:
		return cAccent
	case mlShape:
		return cGreen
	}
	return ""
}

// mlBlock is one building block: how it colors, how it reads back in a preview,
// the PyTorch constructor it exports to, and how many parameters it holds. A new
// block is one add() line in buildMLBlocks — that one line teaches the type to
// color it, preview it, export it, and count it.
type mlBlock struct {
	name string // canonical name, shown in previews (Conv2d, Attention)
	kind mlKind
	// flow marks a block whose first two positional arguments are in→out sizes,
	// so previews read "Linear(784→256)" instead of "Linear(784, 256)".
	flow bool
	// torch returns the constructor call, e.g. "nn.Linear(784, 256)". "" means
	// PyTorch has no direct module for it and the export falls back to
	// nn.Identity() carrying the row's text as a comment.
	torch func(a mlArgs) string
	// params is the block's parameter count for the given arguments (weights +
	// biases). nil = parameter-free (activations, pooling, dropout).
	params func(a mlArgs) int
	// preview overrides the generic "Name(args)" reading. nil = the default.
	preview func(a mlArgs) string
}

// mlArgs are a block row's arguments, parsed from everything after the block
// name. Both spellings work — positional ("Linear 784 256", "Linear 784→256")
// and named ("Conv2d 3 64 k=3 s=2 p=1") — and floats are kept as floats so
// "Dropout 0.1" survives the round trip.
type mlArgs struct {
	pos []float64 // positional numbers, in the order typed
	kv  []mlKV    // named arguments, in the order typed
	raw []string  // non-numeric leftovers, kept verbatim for the export comment
}

type mlKV struct {
	k string
	v float64
}

// arg resolves one argument: the i-th positional if present (i < 0 to skip the
// positional form entirely), else the first of keys that was named, else def.
func (a mlArgs) arg(i int, def float64, keys ...string) float64 {
	if i >= 0 && i < len(a.pos) {
		return a.pos[i]
	}
	for _, k := range keys {
		for _, kv := range a.kv {
			if kv.k == k {
				return kv.v
			}
		}
	}
	return def
}

func (a mlArgs) argI(i int, def int, keys ...string) int {
	return int(a.arg(i, float64(def), keys...))
}

// mlBlocks is the canonical block table (lowercase canonical name → block) and
// mlAlias maps every accepted spelling onto it, so "fc", "dense" and "Linear"
// are the same block and "attn" lights up the moment it is typed.
var mlBlocks, mlAlias = buildMLBlocks()

func buildMLBlocks() (map[string]mlBlock, map[string]string) {
	blocks := map[string]mlBlock{}
	alias := map[string]string{}
	add := func(b mlBlock, aliases ...string) {
		key := strings.ToLower(b.name)
		blocks[key] = b
		alias[key] = key
		for _, a := range aliases {
			alias[strings.ToLower(a)] = key
		}
	}
	// a parameter-free module (activation, pooling, dropout): one constructor,
	// no weights. args are still parsed so "LeakyReLU 0.2" exports its slope.
	plain := func(name string, kind mlKind, torch func(a mlArgs) string, aliases ...string) {
		add(mlBlock{name: name, kind: kind, torch: torch}, aliases...)
	}
	// nullary is the common plain block: nn.ReLU(), nn.Flatten().
	nullary := func(name string, kind mlKind, aliases ...string) {
		plain(name, kind, func(a mlArgs) string { return mlCall(name) }, aliases...)
	}

	// ── weighted layers ───────────────────────────────────────────────────
	add(mlBlock{
		name: "Linear", kind: mlWeighted, flow: true,
		torch: func(a mlArgs) string {
			args := []string{mlNum(a.arg(0, 0, "in", "in_features")), mlNum(a.arg(1, 0, "out", "out_features"))}
			return mlCall("Linear", append(args, mlBiasArg(a)...)...)
		},
		params: func(a mlArgs) int {
			in, out := a.argI(0, 0, "in", "in_features"), a.argI(1, 0, "out", "out_features")
			return in*out + mlBias(a, out)
		},
	}, "dense", "fc", "fullyconnected", "proj")

	for _, c := range []struct {
		name string
		dim  int
	}{{"Conv1d", 1}, {"Conv2d", 2}, {"Conv3d", 3}} {
		add(mlConvBlock(c.name, c.dim, false))
	}
	add(mlConvBlock("ConvTranspose2d", 2, true), "deconv", "upconv")
	alias["conv"] = "conv2d"
	alias["convolution"] = "conv2d"

	add(mlBlock{
		// a depthwise conv is one channel per group: cheap, and its parameter
		// count is the reason MobileNet-shaped models fit on a phone.
		name: "DepthwiseConv2d", kind: mlWeighted,
		torch: func(a mlArgs) string {
			c := a.arg(0, 0, "c", "channels")
			k := a.arg(1, 3, "k", "kernel", "kernel_size")
			args := []string{mlNum(c), mlNum(c), mlKw("kernel_size", k), mlKw("groups", c)}
			if p := a.arg(-1, 0, "p", "pad", "padding"); p != 0 {
				args = append(args, mlKw("padding", p))
			}
			return mlCall("Conv2d", append(args, mlBiasArg(a)...)...)
		},
		params: func(a mlArgs) int {
			c, k := a.argI(0, 0, "c", "channels"), a.argI(1, 3, "k", "kernel", "kernel_size")
			return c*k*k + mlBias(a, c)
		},
	}, "dwconv", "depthwise")

	add(mlBlock{
		name: "Embedding", kind: mlWeighted, flow: true,
		torch: func(a mlArgs) string {
			return mlCall("Embedding", mlNum(a.arg(0, 0, "n", "num", "vocab")), mlNum(a.arg(1, 0, "d", "dim")))
		},
		params: func(a mlArgs) int { return a.argI(0, 0, "n", "num", "vocab") * a.argI(1, 0, "d", "dim") },
	}, "embed", "emb", "wte", "tokenembedding")

	add(mlBlock{
		name: "PosEmbed", kind: mlWeighted, flow: true,
		torch: func(a mlArgs) string {
			return mlCall("Embedding", mlNum(a.arg(0, 0, "n", "len", "seq")), mlNum(a.arg(1, 0, "d", "dim")))
		},
		params: func(a mlArgs) int { return a.argI(0, 0, "n", "len", "seq") * a.argI(1, 0, "d", "dim") },
	}, "positional", "posemb", "positionalembedding", "wpe", "positionalencoding")

	add(mlBlock{
		// attention is quoted by its model dimension and head count; the four
		// projections (q, k, v, out) are what the parameter count sums.
		name: "Attention", kind: mlWeighted,
		torch: func(a mlArgs) string {
			// the SelfAttention wrapper (mlHelperSrc), not a bare
			// nn.MultiheadAttention: MHA takes three inputs and returns a tuple, so
			// only the wrapped form actually forwards inside an nn.Sequential.
			d := a.arg(0, 0, "d", "dim", "embed_dim")
			h := a.arg(1, 8, "h", "heads", "num_heads")
			return "SelfAttention(" + mlNum(d) + ", " + mlNum(h) + ")"
		},
		params: func(a mlArgs) int {
			d := a.argI(0, 0, "d", "dim", "embed_dim")
			return 4*d*d + 4*d
		},
		preview: func(a mlArgs) string {
			d := a.arg(0, 0, "d", "dim", "embed_dim")
			h := a.arg(1, 8, "h", "heads", "num_heads")
			return "Attention(" + mlNum(d) + ", " + mlNum(h) + "h)"
		},
	}, "attn", "mha", "multiheadattention", "selfattention", "causalselfattention", "crossattention", "sdpa")

	add(mlBlock{
		// the transformer's position-wise FFN: one expansion, one projection
		// back. mult is the expansion factor (4 in nearly every paper).
		name: "FeedForward", kind: mlWeighted,
		torch: func(a mlArgs) string {
			d, h := a.argI(0, 0, "d", "dim"), mlFFHidden(a)
			return "nn.Sequential(" + mlCall("Linear", mlNum(float64(d)), mlNum(float64(h))) + ", " + mlCall("GELU") + ", " +
				mlCall("Linear", mlNum(float64(h)), mlNum(float64(d))) + ")"
		},
		params: func(a mlArgs) int {
			d, h := a.argI(0, 0, "d", "dim"), mlFFHidden(a)
			return 2*d*h + h + d
		},
	}, "ffn", "ff", "mlpblock", "mlp")

	for _, r := range []struct {
		name  string
		gates int
	}{{"LSTM", 4}, {"GRU", 3}, {"RNN", 1}} {
		add(mlRecurrentBlock(r.name, r.gates))
	}

	add(mlBlock{
		// a ViT patch embedding is a stride-p convolution: each p×p patch
		// becomes one token of width d.
		name: "PatchEmbed", kind: mlWeighted, flow: true,
		torch: func(a mlArgs) string {
			c, d := a.arg(0, 3, "c", "channels"), a.arg(1, 0, "d", "dim")
			p := a.arg(2, 16, "p", "patch")
			return mlCall("Conv2d", mlNum(c), mlNum(d), mlKw("kernel_size", p), mlKw("stride", p))
		},
		params: func(a mlArgs) int {
			c, d := a.argI(0, 3, "c", "channels"), a.argI(1, 0, "d", "dim")
			p := a.argI(2, 16, "p", "patch")
			return c*p*p*d + d
		},
	}, "patch", "patchembedding")

	// ── normalization and regularization ──────────────────────────────────
	add(mlBlock{
		name: "LayerNorm", kind: mlNorm,
		torch:  func(a mlArgs) string { return mlCall("LayerNorm", mlNum(a.arg(0, 0, "d", "dim"))) },
		params: func(a mlArgs) int { return 2 * a.argI(0, 0, "d", "dim") },
	}, "ln", "layernorm2d")
	add(mlBlock{
		name: "RMSNorm", kind: mlNorm,
		torch:  func(a mlArgs) string { return mlCall("RMSNorm", mlNum(a.arg(0, 0, "d", "dim"))) },
		params: func(a mlArgs) int { return a.argI(0, 0, "d", "dim") },
	}, "rms")
	for _, n := range []string{"BatchNorm1d", "BatchNorm2d", "BatchNorm3d"} {
		add(mlBlock{
			name: n, kind: mlNorm,
			torch:  func(name string) func(mlArgs) string { return func(a mlArgs) string { return mlCall(name, mlNum(a.arg(0, 0, "c", "channels"))) } }(n),
			params: func(a mlArgs) int { return 2 * a.argI(0, 0, "c", "channels") },
		})
	}
	alias["bn"] = "batchnorm2d"
	alias["batchnorm"] = "batchnorm2d"
	add(mlBlock{
		name: "GroupNorm", kind: mlNorm,
		torch: func(a mlArgs) string {
			return mlCall("GroupNorm", mlNum(a.arg(0, 32, "g", "groups")), mlNum(a.arg(1, 0, "c", "channels")))
		},
		params: func(a mlArgs) int { return 2 * a.argI(1, 0, "c", "channels") },
	}, "gn")
	plain("InstanceNorm2d", mlNorm, func(a mlArgs) string {
		return mlCall("InstanceNorm2d", mlNum(a.arg(0, 0, "c", "channels")))
	}, "in2d")
	plain("Dropout", mlNorm, func(a mlArgs) string {
		return mlCall("Dropout", mlNum(a.arg(0, 0.1, "p")))
	}, "drop")
	plain("Dropout2d", mlNorm, func(a mlArgs) string {
		return mlCall("Dropout2d", mlNum(a.arg(0, 0.1, "p")))
	})

	// ── activations ───────────────────────────────────────────────────────
	for _, act := range []string{"ReLU", "GELU", "SiLU", "Tanh", "Sigmoid", "ELU", "Mish", "Softplus", "Hardswish", "ReLU6"} {
		nullary(act, mlAct)
	}
	alias["swish"] = "silu"
	plain("LeakyReLU", mlAct, func(a mlArgs) string {
		return mlCall("LeakyReLU", mlNum(a.arg(0, 0.01, "slope", "negative_slope")))
	}, "lrelu")
	plain("Softmax", mlAct, func(a mlArgs) string {
		return mlCall("Softmax", mlKw("dim", a.arg(0, -1, "dim")))
	})
	plain("LogSoftmax", mlAct, func(a mlArgs) string {
		return mlCall("LogSoftmax", mlKw("dim", a.arg(0, -1, "dim")))
	})
	plain("GLU", mlAct, func(a mlArgs) string { return mlCall("GLU") })

	// ── shape ops ─────────────────────────────────────────────────────────
	nullary("Flatten", mlShape)
	nullary("Identity", mlShape)
	plain("MaxPool2d", mlShape, func(a mlArgs) string {
		return mlCall("MaxPool2d", mlNum(a.arg(0, 2, "k", "kernel", "kernel_size")))
	}, "maxpool")
	plain("AvgPool2d", mlShape, func(a mlArgs) string {
		return mlCall("AvgPool2d", mlNum(a.arg(0, 2, "k", "kernel", "kernel_size")))
	}, "avgpool")
	plain("AdaptiveAvgPool2d", mlShape, func(a mlArgs) string {
		return mlCall("AdaptiveAvgPool2d", mlNum(a.arg(0, 1, "out")))
	}, "gap", "globalavgpool", "adaptiveavgpool")
	plain("Upsample", mlShape, func(a mlArgs) string {
		return mlCall("Upsample", mlKw("scale_factor", a.arg(0, 2, "scale", "factor")))
	}, "up")
	plain("PixelShuffle", mlShape, func(a mlArgs) string {
		return mlCall("PixelShuffle", mlNum(a.arg(0, 2, "r", "factor")))
	})
	// shape ops PyTorch has no module for: they export as nn.Identity() with the
	// row's text as a comment, so the snippet stays runnable and the intent stays
	// visible rather than being silently dropped.
	for _, s := range []string{"Reshape", "Permute", "Transpose", "Split", "Slice", "Mask"} {
		add(mlBlock{name: s, kind: mlShape})
	}

	return blocks, alias
}

// mlConvBlock builds the Conv1d/2d/3d (and transpose) entry: same argument
// shape, same parameter formula, different rank.
func mlConvBlock(name string, dim int, transpose bool) mlBlock {
	return mlBlock{
		name: name, kind: mlWeighted, flow: true,
		torch: func(a mlArgs) string {
			in, out := a.arg(0, 0, "in", "in_channels"), a.arg(1, 0, "out", "out_channels")
			args := []string{mlNum(in), mlNum(out), mlKw("kernel_size", a.arg(2, 3, "k", "kernel", "kernel_size"))}
			if s := a.arg(-1, 1, "s", "stride"); s != 1 {
				args = append(args, mlKw("stride", s))
			}
			if p := a.arg(-1, 0, "p", "pad", "padding"); p != 0 {
				args = append(args, mlKw("padding", p))
			}
			if g := a.arg(-1, 1, "g", "groups"); g != 1 {
				args = append(args, mlKw("groups", g))
			}
			return mlCall(name, append(args, mlBiasArg(a)...)...)
		},
		params: func(a mlArgs) int {
			in, out := a.argI(0, 0, "in", "in_channels"), a.argI(1, 0, "out", "out_channels")
			k := a.argI(2, 3, "k", "kernel", "kernel_size")
			g := a.argI(-1, 1, "g", "groups")
			if g < 1 {
				g = 1
			}
			kern := 1
			for i := 0; i < dim; i++ {
				kern *= k
			}
			return in/g*out*kern + mlBias(a, out)
		},
	}
}

// mlRecurrentBlock builds LSTM/GRU/RNN: gates × (input, recurrent, two biases)
// for the first layer, then gates × (recurrent, recurrent, two biases) for each
// stacked layer above it.
func mlRecurrentBlock(name string, gates int) mlBlock {
	return mlBlock{
		name: name, kind: mlWeighted, flow: true,
		torch: func(a mlArgs) string {
			in, h := a.arg(0, 0, "in", "input_size"), a.arg(1, 0, "h", "hidden", "hidden_size")
			args := []string{mlNum(in), mlNum(h)}
			if l := a.arg(2, 1, "layers", "num_layers"); l != 1 {
				args = append(args, mlKw("num_layers", l))
			}
			if a.arg(-1, 0, "bidirectional", "bi") != 0 {
				args = append(args, "bidirectional=True")
			}
			return mlCall(name, append(args, "batch_first=True")...)
		},
		params: func(a mlArgs) int {
			in := a.argI(0, 0, "in", "input_size")
			h := a.argI(1, 0, "h", "hidden", "hidden_size")
			layers := a.argI(2, 1, "layers", "num_layers")
			if layers < 1 {
				layers = 1
			}
			total := 0
			for l := 0; l < layers; l++ {
				src := h
				if l == 0 {
					src = in
				}
				total += gates * (src*h + h*h + 2*h)
			}
			if a.arg(-1, 0, "bidirectional", "bi") != 0 {
				total *= 2
			}
			return total
		},
	}
}

// mlFFHidden is a feed-forward block's inner width. A value of 16 or less reads
// as the MULTIPLIER people quote ("FeedForward 768 mult=4" → 3072), since no
// real FFN is sixteen units wide; anything larger is the width itself. The
// default is the 4× of the transformer papers.
func mlFFHidden(a mlArgs) int {
	d := a.argI(0, 0, "d", "dim")
	h := a.argI(1, 4*d, "hidden", "mult")
	if h > 0 && h <= 16 {
		h *= d
	}
	return h
}

// mlBias adds the per-output bias term unless the row says bias=0.
func mlBias(a mlArgs, out int) int {
	if a.arg(-1, 1, "bias") == 0 {
		return 0
	}
	return out
}

// mlBiasArg carries "bias=false" into the exported constructor, so the code and
// the parameter count never describe different models.
func mlBiasArg(a mlArgs) []string {
	if a.arg(-1, 1, "bias") == 0 {
		return []string{"bias=False"}
	}
	return nil
}

// mlLookup resolves a token (any accepted spelling, any case) to its block.
func mlLookup(tok string) (mlBlock, bool) {
	canon, ok := mlAlias[strings.ToLower(tok)]
	if !ok {
		return mlBlock{}, false
	}
	b, ok := mlBlocks[canon]
	return b, ok
}

// ── containers ─────────────────────────────────────────────────────────────

// mlContainer is a composition shape: how a node's children combine. It is the
// ML equivalent of a math operator, and it is what makes a subtree a model
// rather than a list of layers.
type mlContainer int

const (
	mlSeq      mlContainer = iota // children run in order (also the fallback for a named group)
	mlResidual                    // out = x + body(x)
	mlRepeat                      // the body, stacked n times ("12×")
	mlParallel                    // branches applied to the same input, concatenated
	mlAdd                         // branches applied to the same input, summed
)

// mlContainerOf reads a node's text as a container. Besides the words
// (sequential, residual, concat, add) it accepts the count forms "12×", "×12"
// and "repeat 12", which is how a stack of transformer blocks actually reads in
// an outline.
func mlContainerOf(name string) (c mlContainer, n int, ok bool) {
	f := mlSplit(name)
	if len(f) == 0 {
		return 0, 0, false
	}
	head := strings.ToLower(f[0])
	if n, ok := mlRepeatCount(head); ok {
		return mlRepeat, n, true
	}
	switch head {
	case "sequential", "seq", "chain":
		return mlSeq, 0, true
	case "residual", "res", "skip", "shortcut":
		return mlResidual, 0, true
	case "repeat", "stack":
		n := 1
		if len(f) > 1 {
			if v, err := strconv.Atoi(strings.Trim(f[1], "×x")); err == nil {
				n = v
			}
		}
		return mlRepeat, n, true
	case "parallel", "concat", "cat", "branch":
		return mlParallel, 0, true
	case "add", "sum", "merge":
		return mlAdd, 0, true
	}
	return 0, 0, false
}

// mlRepeatCount reads the bare count forms "12×", "12x" and "×12".
func mlRepeatCount(tok string) (int, bool) {
	for _, mark := range []string{"×", "x", "*"} {
		if s := strings.TrimSuffix(tok, mark); s != tok {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				return n, true
			}
		}
		if s := strings.TrimPrefix(tok, mark); s != tok {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}

// ── parsing a row ──────────────────────────────────────────────────────────

// mlSplit tokenizes a row. Punctuation people naturally type around arguments —
// parens, commas, and the in→out arrow in both spellings — separates tokens, so
// "Linear(784, 256)", "Linear 784→256" and "Linear 784 256" all parse the same.
func mlSplit(s string) []string {
	s = strings.ReplaceAll(s, "->", " ")
	s = strings.ReplaceAll(s, "→", " ")
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ' ', '\t', '(', ')', ',', ';', '[', ']':
			return true
		}
		return false
	})
}

// mlParse reads a row as a block plus its arguments. ok=false means the text is
// not a known block — prose, a group name, a container — and the caller decides
// what that means.
func mlParse(name string) (mlBlock, mlArgs, bool) {
	f := mlSplit(name)
	if len(f) == 0 {
		return mlBlock{}, mlArgs{}, false
	}
	b, ok := mlLookup(f[0])
	if !ok {
		return mlBlock{}, mlArgs{}, false
	}
	return b, mlParseArgs(f[1:]), true
}

// mlParseArgs sorts the tokens after a block name into positional numbers,
// named numbers, and everything else (kept verbatim for the export comment).
func mlParseArgs(toks []string) mlArgs {
	var a mlArgs
	for _, t := range toks {
		if i := strings.IndexAny(t, "=:"); i > 0 {
			k := strings.ToLower(t[:i])
			if v, err := strconv.ParseFloat(t[i+1:], 64); err == nil {
				a.kv = append(a.kv, mlKV{k, v})
				continue
			}
			if b, ok := mlBoolWord(t[i+1:]); ok {
				a.kv = append(a.kv, mlKV{k, b})
				continue
			}
		}
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			a.pos = append(a.pos, v)
			continue
		}
		a.raw = append(a.raw, t)
	}
	return a
}

// mlBoolWord lets "bias=false" and "bidirectional=true" read naturally; the
// numeric channel carries them as 0/1.
func mlBoolWord(s string) (float64, bool) {
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return 1, true
	case "false", "no", "off":
		return 0, true
	}
	return 0, false
}

// ── registry hooks ─────────────────────────────────────────────────────────

// mlSpanColor tints the block name by family and dims its arguments, leaving a
// prose row (a group name like "Encoder") completely untouched. It rides the
// per-rune kwColor channel (see renderBody), so caret and selection keep working.
func mlSpanColor(it *item, runes []rune) map[int]string {
	i := 0
	for i < len(runes) && runes[i] == ' ' {
		i++
	}
	j := i
	for j < len(runes) && runes[j] != ' ' {
		j++
	}
	if j == i {
		return nil
	}
	col, ok := mlTokenColor(string(runes[i:j]))
	if !ok {
		return nil
	}
	out := make(map[int]string, len(runes)-i)
	for k := i; k < j; k++ {
		out[k] = col
	}
	for k := j; k < len(runes); k++ {
		out[k] = cDim // the arguments read as a quiet tail behind the name
	}
	return out
}

// mlTokenColor is the color of a leading token: a container's yellow or a
// block's family tint. ok=false for prose.
func mlTokenColor(tok string) (string, bool) {
	tok = strings.TrimRight(tok, "(,")
	if _, _, ok := mlContainerOf(tok); ok {
		return mlColor(mlContainerK), true
	}
	if b, ok := mlLookup(tok); ok {
		return mlColor(b.kind), true
	}
	return "", false
}

// mlBodyTail is the dim tail on a model row: the linear preview of the node's
// subtree, then its parameter count. A leaf keeps only the count (its own text
// is already the whole layer) — that is the "one layer stays inline" behavior,
// and it is what turns a tree of blocks into a readable model at rest.
func mlBodyTail(it *item) string {
	if it == nil {
		return ""
	}
	var parts []string
	if len(it.children) > 0 {
		if p := mlPreview(it); p != "" {
			parts = append(parts, p)
		}
	}
	if n := mlParams(it); n > 0 {
		parts = append(parts, mlFmtParams(n))
	}
	if len(parts) == 0 {
		return ""
	}
	return cDim + strings.Join(parts, " · ") + cReset
}

// mlToContext gives the node its own <model> element carrying the flattened
// architecture and its size, so structured context reads
// "Conv2d(3→64) → BatchNorm2d(64) → ReLU · 1.9K params" instead of a bare
// container word; the child blocks still nest inside.
func mlToContext(it *item) contextXML {
	body := mlPreview(it)
	if n := mlParams(it); n > 0 {
		if body != "" {
			body += " · "
		}
		body += mlFmtParams(n) + " params"
	}
	return contextXML{tag: "model", body: body}
}

// runMLTorch (alt+r) exports the node's subtree as PyTorch into its ephemeral
// run band. Because it serializes THIS node down, running it on any sub-node
// yields the module for just that part of the architecture.
func runMLTorch(m *Model, it *item) tea.Cmd {
	r := m.ensureRun(it.uuid)
	r.out = nil
	for _, l := range strings.Split(mlToTorch(it), "\n") {
		r.out = append(r.out, outLine{text: l})
	}
	r.dropped = 0
	m.persistRunOut(it.uuid)
	m.flash = "PyTorch → output"
	m.refreshRows()
	return nil
}

// mlFlashActions names the alt+r action "torch" in the flash bar.
func mlFlashActions(m *Model, it *item) []flashAction {
	return []flashAction{{verb: "torch", color: cGreen, do: runMLTorch}}
}

// ── preview: subtree → one linear line ─────────────────────────────────────

// mlPreviewMax clips the tail preview: a deep model would otherwise push a row
// far past the terminal width, and the tail is a glance, not the model.
const mlPreviewMax = 72

// mlPreview flattens a model node's subtree into a compact one-line form. The
// node's OWN name is left out — it is already on the row — so a "12×" row reads
// "12× · LayerNorm(768) → Attention(768, 12h) → …". Pure/recursive; the same
// routine feeds the inline tail and structured context.
func mlPreview(it *item) string {
	if it == nil {
		return ""
	}
	if len(it.children) == 0 {
		return mlLeaf(it)
	}
	kids := mlKidPreviews(it)
	c, _, ok := mlContainerOf(it.name)
	if !ok {
		c = mlSeq // a named group ("Encoder") composes its children in order
	}
	return mlClip(mlCompose(c, kids), mlPreviewMax)
}

// mlPreviewOf renders a node as an OPERAND inside its parent's preview, where
// its own name still matters: a group keeps its label, a repeat keeps its count.
func mlPreviewOf(it *item) string {
	if it == nil {
		return ""
	}
	if len(it.children) == 0 {
		return mlLeaf(it)
	}
	body := mlCompose(mlContainerKindOf(it), mlKidPreviews(it))
	switch c, n, ok := mlContainerOf(it.name); {
	case !ok:
		return strings.TrimSpace(it.name) + "(" + body + ")" // a named group
	case c == mlRepeat:
		return strconv.Itoa(n) + "× (" + body + ")"
	case c == mlSeq && strings.Contains(body, " → "):
		return "(" + body + ")"
	}
	return body
}

func mlContainerKindOf(it *item) mlContainer {
	if c, _, ok := mlContainerOf(it.name); ok {
		return c
	}
	return mlSeq
}

func mlKidPreviews(it *item) []string {
	kids := make([]string, 0, len(it.children))
	for _, c := range it.children {
		if p := mlPreviewOf(c); p != "" {
			kids = append(kids, p)
		}
	}
	return kids
}

// mlCompose joins already-rendered children by their container's shape.
func mlCompose(c mlContainer, kids []string) string {
	switch c {
	case mlResidual:
		return "x + (" + strings.Join(kids, " → ") + ")"
	case mlParallel:
		return "[" + strings.Join(kids, " | ") + "]"
	case mlAdd:
		return strings.Join(kids, " + ")
	}
	return strings.Join(kids, " → ") // mlSeq, and a repeat's own body
}

// mlLeaf renders one block row: its canonical name with the arguments as typed
// ("Linear 784 256" → "Linear(784→256)"). Prose passes through untouched.
func mlLeaf(it *item) string {
	name := strings.TrimSpace(it.name)
	b, a, ok := mlParse(name)
	if !ok {
		return name
	}
	if b.preview != nil {
		return b.preview(a)
	}
	var parts []string
	pos := a.pos
	if b.flow && len(pos) >= 2 {
		parts = append(parts, mlNum(pos[0])+"→"+mlNum(pos[1]))
		pos = pos[2:]
	}
	for _, p := range pos {
		parts = append(parts, mlNum(p))
	}
	for _, kv := range a.kv {
		parts = append(parts, kv.k+"="+mlNum(kv.v))
	}
	if len(parts) == 0 {
		return b.name
	}
	return b.name + "(" + strings.Join(parts, ", ") + ")"
}

func mlClip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimRight(string(r[:max-1]), " →|+") + "…"
}

// ── parameters ─────────────────────────────────────────────────────────────

// mlParams is the parameter count of a node's whole subtree: its own block's
// weights plus every child's, with a repeat container multiplying its body. It
// is the number that tells you whether a model fits in memory, and it comes from
// the same argument parse the code export uses — so the count and the code can
// never disagree.
func mlParams(it *item) int {
	if it == nil {
		return 0
	}
	total := 0
	if b, a, ok := mlParse(it.name); ok && b.params != nil {
		total += b.params(a)
	}
	kids := 0
	for _, c := range it.children {
		kids += mlParams(c)
	}
	if c, n, ok := mlContainerOf(it.name); ok && c == mlRepeat && n > 1 {
		kids *= n
	}
	return total + kids
}

// mlFmtParams renders a parameter count the way model cards do: 1.2K, 38.9M,
// 175B — three significant digits, no trailing ".0".
func mlFmtParams(n int) string {
	switch {
	case n <= 0:
		return ""
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		return mlScaled(float64(n)/1000, "K")
	case n < 1_000_000_000:
		return mlScaled(float64(n)/1_000_000, "M")
	}
	return mlScaled(float64(n)/1_000_000_000, "B")
}

func mlScaled(v float64, unit string) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	return s + unit
}

// ── PyTorch: subtree → nn.Module code ──────────────────────────────────────

// mlHelpers records which support classes the export used, so a snippet carries
// exactly the classes it needs and nothing else.
type mlHelpers struct{ residual, parallel, add, attn bool }

// mlToTorch serializes a model node's subtree to runnable PyTorch. Blocks become
// their constructors, containers become nn.Sequential / a small support class,
// and a repeat becomes a list comprehension — the shapes people actually write
// by hand. Unknown rows survive as nn.Identity() carrying their text, so a
// half-written model still exports something you can run.
func mlToTorch(it *item) string {
	if it == nil {
		return ""
	}
	var h mlHelpers
	body := mlTorchLines(it, &h)
	if len(body) == 0 {
		return ""
	}
	out := []string{"import torch", "import torch.nn as nn", ""}
	out = append(out, mlHelperSrc(h)...)
	if head := mlTorchHeader(it); head != "" {
		out = append(out, head)
	}
	body[0] = "model = " + body[0]
	out = append(out, body...)
	return strings.Join(out, "\n")
}

// mlTorchHeader is the one comment line above the model: its name (when the row
// is a label rather than a block) and its parameter count.
func mlTorchHeader(it *item) string {
	var parts []string
	if name := strings.TrimSpace(it.name); name != "" {
		if _, _, isBlock := mlParse(name); !isBlock {
			if _, _, isContainer := mlContainerOf(name); !isContainer {
				parts = append(parts, name)
			}
		}
	}
	if n := mlParams(it); n > 0 {
		parts = append(parts, mlFmtParams(n)+" params")
	}
	if len(parts) == 0 {
		return ""
	}
	return "# " + strings.Join(parts, " · ")
}

// mlTorchLines renders one node as an expression. The first line carries no
// indentation and the last line no trailing comma — the caller places both, so
// the same routine serves the top level and every nesting depth.
func mlTorchLines(it *item, h *mlHelpers) []string {
	if it == nil {
		return nil
	}
	if len(it.children) == 0 {
		return []string{mlTorchLeafH(it, h)}
	}
	kids := make([][]string, 0, len(it.children))
	for _, c := range it.children {
		if ls := mlTorchLines(c, h); len(ls) > 0 {
			kids = append(kids, ls)
		}
	}
	if len(kids) == 0 {
		return []string{mlTorchLeafH(it, h)}
	}
	c, n, isContainer := mlContainerOf(it.name)
	if !isContainer {
		c = mlSeq
	}
	switch c {
	case mlResidual:
		// one child needs no nn.Sequential around it: Residual(nn.Linear(…)).
		h.residual = true
		body := kids
		if len(kids) > 1 {
			body = [][]string{mlSeqLines(kids, "")}
		}
		return mlWrapCall("Residual", body, "")
	case mlParallel:
		h.parallel = true
		return mlWrapCall("Concat", kids, "")
	case mlAdd:
		h.add = true
		return mlWrapCall("Add", kids, "")
	case mlRepeat:
		// n copies of the body: "nn.Sequential(*[<body> for _ in range(n)])" is
		// valid both nested and at the top level, unlike a bare starred list.
		// One child is its own body — no nn.Sequential wrapper around a single
		// block, which is what makes a stack of transformer blocks read cleanly.
		inner := kids[0]
		if len(kids) > 1 {
			inner = mlSeqLines(kids, "")
		}
		inner = append([]string{}, inner...)
		inner[0] = "nn.Sequential(*[" + inner[0]
		inner[len(inner)-1] += " for _ in range(" + strconv.Itoa(n) + ")])"
		return inner
	}
	return mlSeqLines(kids, mlGroupComment(it))
}

// mlSeqLines lays children out as an nn.Sequential block, one child per line.
func mlSeqLines(kids [][]string, comment string) []string {
	head := "nn.Sequential("
	if comment != "" {
		head += "  " + comment
	}
	out := []string{head}
	for _, k := range kids {
		out = append(out, mlIndent(k, ",")...)
	}
	return append(out, ")")
}

// mlWrapCall lays children out as arguments of a support class: Residual(…),
// Concat(…), Add(…).
func mlWrapCall(name string, kids [][]string, comment string) []string {
	head := name + "("
	if comment != "" {
		head += "  " + comment
	}
	out := []string{head}
	for _, k := range kids {
		out = append(out, mlIndent(k, ",")...)
	}
	return append(out, ")")
}

// mlIndent shifts a child expression one level in and appends the separator to
// its last line — BEFORE any trailing comment, so a commented row still gets its
// comma and the snippet stays valid Python.
func mlIndent(lines []string, sep string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "    " + l
	}
	last := len(out) - 1
	if i := strings.Index(out[last], mlComment); i >= 0 {
		out[last] = out[last][:i] + sep + out[last][i:]
	} else {
		out[last] += sep
	}
	return out
}

// mlComment is the separator between an exported call and the row text kept
// beside it.
const mlComment = "  # "

// mlGroupComment labels a named group's nn.Sequential with the row's own text.
func mlGroupComment(it *item) string {
	name := strings.TrimSpace(it.name)
	if name == "" {
		return ""
	}
	if _, _, ok := mlContainerOf(name); ok {
		return ""
	}
	if _, _, ok := mlParse(name); ok {
		return ""
	}
	return "# " + name
}

// mlTorchLeaf renders one block row as its constructor. A row PyTorch has no
// module for — prose, or a shape op like Permute — becomes nn.Identity() with
// the text as a comment, so the snippet runs and nothing typed is lost.
func mlTorchLeaf(it *item) string {
	name := strings.TrimSpace(it.name)
	b, a, ok := mlParse(name)
	if !ok || b.torch == nil {
		if name == "" {
			return mlCall("Identity")
		}
		return mlCall("Identity") + mlComment + name
	}
	call := b.torch(a)
	if len(a.raw) > 0 {
		call += mlComment + strings.Join(a.raw, " ")
	}
	return call
}

// mlTorchLeafH renders a leaf and records the support classes its constructor
// needs, so a snippet carries exactly the classes it calls.
func mlTorchLeafH(it *item, h *mlHelpers) string {
	line := mlTorchLeaf(it)
	if strings.Contains(line, "SelfAttention(") {
		h.attn = true
	}
	return line
}

// mlHelperSrc emits only the support classes the snippet actually used.
func mlHelperSrc(h mlHelpers) []string {
	var out []string
	if h.attn {
		out = append(out,
			"class SelfAttention(nn.Module):",
			"    def __init__(self, dim, heads):",
			"        super().__init__()",
			"        self.attn = nn.MultiheadAttention(dim, heads, batch_first=True)",
			"",
			"    def forward(self, x):",
			"        out, _ = self.attn(x, x, x)",
			"        return out",
			"", "")
	}
	if h.residual {
		out = append(out,
			"class Residual(nn.Module):",
			"    def __init__(self, body):",
			"        super().__init__()",
			"        self.body = body",
			"",
			"    def forward(self, x):",
			"        return x + self.body(x)",
			"", "")
	}
	if h.parallel {
		out = append(out,
			"class Concat(nn.Module):",
			"    def __init__(self, *branches):",
			"        super().__init__()",
			"        self.branches = nn.ModuleList(branches)",
			"",
			"    def forward(self, x):",
			"        return torch.cat([b(x) for b in self.branches], dim=1)",
			"", "")
	}
	if h.add {
		out = append(out,
			"class Add(nn.Module):",
			"    def __init__(self, *branches):",
			"        super().__init__()",
			"        self.branches = nn.ModuleList(branches)",
			"",
			"    def forward(self, x):",
			"        return sum(b(x) for b in self.branches)",
			"", "")
	}
	return out
}

// ── small formatting helpers ───────────────────────────────────────────────

// mlCall builds a torch.nn constructor call.
func mlCall(name string, args ...string) string {
	return "nn." + name + "(" + strings.Join(args, ", ") + ")"
}

// mlKw builds a keyword argument, e.g. kernel_size=3.
func mlKw(k string, v float64) string { return k + "=" + mlNum(v) }

// mlNum prints an argument the way it was meant: an integer size as an integer,
// a rate like 0.1 as itself.
func mlNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
