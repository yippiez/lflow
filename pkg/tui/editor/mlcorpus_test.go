package editor

import (
	"strings"
	"testing"
)

// mlCorpus is a shelf of famous architectures written the way you'd type them
// into a Model outline — one building block per row, containers for the shapes
// that are not a straight line. It is the coverage net for the type, exactly as
// the math corpus is for Math: every leaf row must resolve to a known block (an
// unknown one fails loudly, which is how new blocks get added), every model must
// export to runnable PyTorch, and the ones with a published parameter count must
// land on that number.
//
// It is also the documentation. These trees are what the type is FOR, and they
// are meant to be read as much as run.
var mlCorpus = []struct {
	name string
	arch string
	// params is the published parameter count, 0 when the entry is a fragment or
	// the published number is not meaningful. tol is the allowed relative slack:
	// a corpus model is a faithful sketch, not a checkpoint.
	params int
	tol    float64
	// prose lists leaf rows that are deliberately not blocks — a step the tree
	// cannot express, kept as a readable note instead of being faked.
	prose []string
}{
	{
		name: "logistic regression",
		arch: `
Logistic regression
  Linear 784 10
  Softmax`,
		params: 7850, tol: 0.001,
	},
	{
		name: "MNIST MLP",
		arch: `
MNIST MLP
  Flatten
  Linear 784 256
  ReLU
  Dropout 0.2
  Linear 256 10`,
		params: 203_530, tol: 0.001,
	},
	{
		name: "LeNet-5",
		arch: `
LeNet-5
  Conv2d 1 6 k=5 p=2
  Tanh
  AvgPool2d 2
  Conv2d 6 16 k=5
  Tanh
  AvgPool2d 2
  Flatten
  Linear 400 120
  Tanh
  Linear 120 84
  Tanh
  Linear 84 10`,
		params: 61_706, tol: 0.01,
	},
	{
		name: "AlexNet",
		arch: `
AlexNet
  features
    Conv2d 3 64 k=11 s=4 p=2
    ReLU
    MaxPool2d 3
    Conv2d 64 192 k=5 p=2
    ReLU
    MaxPool2d 3
    Conv2d 192 384 k=3 p=1
    ReLU
    Conv2d 384 256 k=3 p=1
    ReLU
    Conv2d 256 256 k=3 p=1
    ReLU
    MaxPool2d 3
  classifier
    Flatten
    Dropout 0.5
    Linear 9216 4096
    ReLU
    Dropout 0.5
    Linear 4096 4096
    ReLU
    Linear 4096 1000`,
		params: 61_100_840, tol: 0.01,
	},
	{
		name: "VGG-16",
		arch: `
VGG-16
  Conv2d 3 64 k=3 p=1
  ReLU
  Conv2d 64 64 k=3 p=1
  ReLU
  MaxPool2d 2
  Conv2d 64 128 k=3 p=1
  ReLU
  Conv2d 128 128 k=3 p=1
  ReLU
  MaxPool2d 2
  Conv2d 128 256 k=3 p=1
  ReLU
  Conv2d 256 256 k=3 p=1
  ReLU
  Conv2d 256 256 k=3 p=1
  ReLU
  MaxPool2d 2
  Conv2d 256 512 k=3 p=1
  ReLU
  Conv2d 512 512 k=3 p=1
  ReLU
  Conv2d 512 512 k=3 p=1
  ReLU
  MaxPool2d 2
  Conv2d 512 512 k=3 p=1
  ReLU
  Conv2d 512 512 k=3 p=1
  ReLU
  Conv2d 512 512 k=3 p=1
  ReLU
  MaxPool2d 2
  Flatten
  Linear 25088 4096
  ReLU
  Dropout 0.5
  Linear 4096 4096
  ReLU
  Dropout 0.5
  Linear 4096 1000`,
		params: 138_357_544, tol: 0.001,
	},
	{
		// the residual container is the whole point of this one: the identity path
		// is implicit, and the downsampling blocks make their projection shortcut
		// explicit as an add of two branches.
		name: "ResNet-18",
		arch: `
ResNet-18
  stem
    Conv2d 3 64 k=7 s=2 p=3
    BatchNorm2d 64
    ReLU
    MaxPool2d 3
  stage 64
    2×
      residual
        Conv2d 64 64 k=3 p=1
        BatchNorm2d 64
        ReLU
        Conv2d 64 64 k=3 p=1
        BatchNorm2d 64
      ReLU
  stage 128
    add
      sequential
        Conv2d 64 128 k=3 s=2 p=1
        BatchNorm2d 128
        ReLU
        Conv2d 128 128 k=3 p=1
        BatchNorm2d 128
      sequential
        Conv2d 64 128 k=1 s=2
        BatchNorm2d 128
    residual
      Conv2d 128 128 k=3 p=1
      BatchNorm2d 128
      ReLU
      Conv2d 128 128 k=3 p=1
      BatchNorm2d 128
  stage 256
    add
      sequential
        Conv2d 128 256 k=3 s=2 p=1
        BatchNorm2d 256
        ReLU
        Conv2d 256 256 k=3 p=1
        BatchNorm2d 256
      sequential
        Conv2d 128 256 k=1 s=2
        BatchNorm2d 256
    residual
      Conv2d 256 256 k=3 p=1
      BatchNorm2d 256
      ReLU
      Conv2d 256 256 k=3 p=1
      BatchNorm2d 256
  stage 512
    add
      sequential
        Conv2d 256 512 k=3 s=2 p=1
        BatchNorm2d 512
        ReLU
        Conv2d 512 512 k=3 p=1
        BatchNorm2d 512
      sequential
        Conv2d 256 512 k=1 s=2
        BatchNorm2d 512
    residual
      Conv2d 512 512 k=3 p=1
      BatchNorm2d 512
      ReLU
      Conv2d 512 512 k=3 p=1
      BatchNorm2d 512
  head
    AdaptiveAvgPool2d 1
    Flatten
    Linear 512 1000`,
		params: 11_689_512, tol: 0.02,
	},
	{
		name: "MobileNet depthwise separable block",
		arch: `
MobileNet depthwise separable block
  DepthwiseConv2d 64 3 p=1
  BatchNorm2d 64
  ReLU6
  Conv2d 64 128 k=1
  BatchNorm2d 128
  ReLU6`,
	},
	{
		name: "Inception module",
		arch: `
Inception module
  concat
    Conv2d 192 64 k=1
    sequential
      Conv2d 192 96 k=1
      ReLU
      Conv2d 96 128 k=3 p=1
    sequential
      Conv2d 192 16 k=1
      ReLU
      Conv2d 16 32 k=5 p=2
    sequential
      MaxPool2d 3
      Conv2d 192 32 k=1`,
	},
	{
		name: "SqueezeNet fire module",
		arch: `
Fire module
  Conv2d 128 16 k=1
  ReLU
  concat
    Conv2d 16 64 k=1
    Conv2d 16 64 k=3 p=1
  ReLU`,
	},
	{
		name: "DenseNet layer",
		arch: `
DenseNet layer
  concat
    Identity
    sequential
      BatchNorm2d 64
      ReLU
      Conv2d 64 128 k=1
      BatchNorm2d 128
      ReLU
      Conv2d 128 32 k=3 p=1`,
	},
	{
		name: "squeeze-and-excitation block",
		arch: `
Squeeze-and-excitation block
  AdaptiveAvgPool2d 1
  Flatten
  Linear 256 16
  ReLU
  Linear 16 256
  Sigmoid`,
	},
	{
		name: "U-Net",
		arch: `
U-Net
  encoder
    down 64
      Conv2d 1 64 k=3 p=1
      ReLU
      Conv2d 64 64 k=3 p=1
      ReLU
    MaxPool2d 2
    down 128
      Conv2d 64 128 k=3 p=1
      ReLU
      Conv2d 128 128 k=3 p=1
      ReLU
    MaxPool2d 2
  bottleneck
    Conv2d 128 256 k=3 p=1
    ReLU
    Conv2d 256 256 k=3 p=1
    ReLU
  decoder
    ConvTranspose2d 256 128 k=2 s=2
    Conv2d 256 128 k=3 p=1
    ReLU
    ConvTranspose2d 128 64 k=2 s=2
    Conv2d 128 64 k=3 p=1
    ReLU
  Conv2d 64 2 k=1`,
	},
	{
		name: "transformer encoder block",
		arch: `
Transformer encoder block
  residual
    Attention 512 heads=8
    Dropout 0.1
  LayerNorm 512
  residual
    FeedForward 512 mult=4
    Dropout 0.1
  LayerNorm 512`,
	},
	{
		name: "BERT-base",
		arch: `
BERT-base
  embeddings
    Embedding 30522 768
    PosEmbed 512 768
    Embedding 2 768
    LayerNorm 768
    Dropout 0.1
  12×
    residual
      Attention 768 heads=12
      Dropout 0.1
    LayerNorm 768
    residual
      FeedForward 768 mult=4
      Dropout 0.1
    LayerNorm 768
  pooler
    Linear 768 768
    Tanh`,
		params: 109_482_240, tol: 0.01,
	},
	{
		// the pre-norm stack, and the model the parameter path is calibrated
		// against: 124,439,808 with the output head tied to the token embedding.
		name: "GPT-2 small",
		arch: gpt2Small,
		prose: []string{
			"lm_head — weights tied to the token embedding",
		},
		params: 124_439_808, tol: 0.001,
	},
	{
		name: "ViT-B/16",
		arch: `
ViT-B/16
  PatchEmbed 3 768 p=16
  PosEmbed 197 768
  Dropout 0.1
  12×
    residual
      LayerNorm 768
      Attention 768 heads=12
    residual
      LayerNorm 768
      FeedForward 768 mult=4
  LayerNorm 768
  Linear 768 1000`,
		params: 86_567_656, tol: 0.01,
	},
	{
		name: "Llama-style decoder block",
		arch: `
Llama block
  residual
    RMSNorm 4096
    Attention 4096 heads=32
  residual
    RMSNorm 4096
    FeedForward 4096 11008`,
	},
	{
		name: "MLP-Mixer block",
		arch: `
MLP-Mixer block
  residual
    LayerNorm 512
    Transpose 1 2
    FeedForward 196 mult=4
    Transpose 1 2
  residual
    LayerNorm 512
    FeedForward 512 mult=4`,
	},
	{
		name: "diffusion U-Net residual block",
		arch: `
Diffusion residual block
  residual
    GroupNorm 32 256
    SiLU
    Conv2d 256 256 k=3 p=1
    GroupNorm 32 256
    SiLU
    Dropout 0.1
    Conv2d 256 256 k=3 p=1
  residual
    GroupNorm 32 256
    Attention 256 heads=8`,
	},
	{
		name: "CLIP dual tower",
		arch: `
CLIP
  image tower
    PatchEmbed 3 768 p=32
    PosEmbed 50 768
    12×
      residual
        LayerNorm 768
        Attention 768 heads=12
      residual
        LayerNorm 768
        FeedForward 768 mult=4
    Linear 768 512 bias=false
  text tower
    Embedding 49408 512
    PosEmbed 77 512
    12×
      residual
        LayerNorm 512
        Attention 512 heads=8
      residual
        LayerNorm 512
        FeedForward 512 mult=4
    Linear 512 512 bias=false`,
	},
	{
		name: "LSTM language model",
		arch: `
LSTM language model
  Embedding 10000 300
  LSTM 300 512 2
  Dropout 0.3
  Linear 512 10000`,
	},
	{
		name: "seq2seq with attention",
		arch: `
Seq2seq with attention
  encoder
    Embedding 32000 512
    GRU 512 512 bidirectional=1
  decoder
    Embedding 32000 512
    GRU 512 512
    Attention 512 heads=1
    Linear 512 32000`,
	},
	{
		name: "word2vec skip-gram",
		arch: `
word2vec skip-gram
  Embedding 100000 300
  Linear 300 100000 bias=false`,
		params: 60_000_000, tol: 0.001,
	},
	{
		name: "autoencoder",
		arch: `
Autoencoder
  encoder
    Linear 784 128
    ReLU
    Linear 128 32
  decoder
    Linear 32 128
    ReLU
    Linear 128 784
    Sigmoid`,
	},
	{
		name: "variational autoencoder",
		arch: `
Variational autoencoder
  encoder
    Linear 784 400
    ReLU
    concat
      Linear 400 20
      Linear 400 20
  reparameterize z = μ + σ ⊙ ε
  decoder
    Linear 20 400
    ReLU
    Linear 400 784
    Sigmoid`,
		prose: []string{"reparameterize z = μ + σ ⊙ ε"},
	},
	{
		name: "DCGAN generator",
		arch: `
DCGAN generator
  ConvTranspose2d 100 512 k=4
  BatchNorm2d 512
  ReLU
  ConvTranspose2d 512 256 k=4 s=2 p=1
  BatchNorm2d 256
  ReLU
  ConvTranspose2d 256 128 k=4 s=2 p=1
  BatchNorm2d 128
  ReLU
  ConvTranspose2d 128 3 k=4 s=2 p=1
  Tanh`,
	},
	{
		name: "DCGAN discriminator",
		arch: `
DCGAN discriminator
  Conv2d 3 128 k=4 s=2 p=1
  LeakyReLU 0.2
  Conv2d 128 256 k=4 s=2 p=1
  BatchNorm2d 256
  LeakyReLU 0.2
  Conv2d 256 512 k=4 s=2 p=1
  BatchNorm2d 512
  LeakyReLU 0.2
  Conv2d 512 1 k=4
  Sigmoid`,
	},
	{
		name: "deep Q-network",
		arch: `
Deep Q-network
  Conv2d 4 32 k=8 s=4
  ReLU
  Conv2d 32 64 k=4 s=2
  ReLU
  Conv2d 64 64 k=3
  ReLU
  Flatten
  Linear 3136 512
  ReLU
  Linear 512 6`,
	},
}

// gpt2Small is quoted by name from the parameter test too — it is the reference
// architecture for the whole counting path.
const gpt2Small = `
GPT-2 small
  Embedding 50257 768
  PosEmbed 1024 768
  Dropout 0.1
  12×
    residual
      LayerNorm 768
      Attention 768 heads=12
    residual
      LayerNorm 768
      FeedForward 768 mult=4
  LayerNorm 768
  lm_head — weights tied to the token embedding`

// Every leaf row in the corpus resolves to a known building block, unless the
// entry declares it as prose. An unknown leaf here means the block table is
// missing something an architecture actually needs — which is how a block gets
// added.
func TestMLCorpusBlocksResolve(t *testing.T) {
	for _, c := range mlCorpus {
		prose := map[string]bool{}
		for _, p := range c.prose {
			prose[p] = true
		}
		var walk func(it *item)
		walk = func(it *item) {
			for _, kid := range it.children {
				walk(kid)
			}
			if len(it.children) > 0 {
				return // a container or a named group, not a block row
			}
			if _, _, ok := mlParse(it.name); ok || prose[it.name] {
				return
			}
			t.Errorf("%s: leaf %q resolves to no building block (add it to buildMLBlocks, or declare it as prose)", c.name, it.name)
		}
		walk(mltree(c.arch))
	}
}

// The published parameter counts are the calibration: they check the block table
// against the real models, not just against itself.
func TestMLCorpusParams(t *testing.T) {
	for _, c := range mlCorpus {
		got := mlParams(mltree(c.arch))
		if got <= 0 {
			t.Errorf("%s: counted no parameters at all", c.name)
			continue
		}
		if c.params == 0 {
			continue
		}
		off := float64(got-c.params) / float64(c.params)
		if off < -c.tol || off > c.tol {
			t.Errorf("%s: %d params (%s), want %d ±%.1f%% (off by %.2f%%)",
				c.name, got, mlFmtParams(got), c.params, c.tol*100, off*100)
		}
	}
}

// Every model exports to a snippet that could be pasted into a file: the imports
// are there, every support class it calls is defined, every row it was given is
// represented, and the parentheses balance.
func TestMLCorpusExports(t *testing.T) {
	for _, c := range mlCorpus {
		root := mltree(c.arch)
		code := mlToTorch(root)
		if !strings.HasPrefix(code, "import torch\nimport torch.nn as nn\n") {
			t.Errorf("%s: export is missing its imports:\n%s", c.name, code)
			continue
		}
		if !strings.Contains(code, "\nmodel = ") {
			t.Errorf("%s: export defines no model", c.name)
		}
		for _, helper := range []string{"Residual", "Concat", "Add", "SelfAttention"} {
			body := code[strings.Index(code, "\nmodel = "):]
			if strings.Contains(body, helper+"(") && !strings.Contains(code, "class "+helper+"(nn.Module):") {
				t.Errorf("%s: export calls %s without defining it", c.name, helper)
			}
		}
		if n, want := strings.Count(code, "nn."), mlLeafCount(root); n < want {
			t.Errorf("%s: export has %d module calls for %d rows — a row was dropped:\n%s", c.name, n, want, code)
		}
		if d := strings.Count(code, "(") - strings.Count(code, ")"); d != 0 {
			t.Errorf("%s: unbalanced parentheses (%+d):\n%s", c.name, d, code)
		}
		if d := strings.Count(code, "[") - strings.Count(code, "]"); d != 0 {
			t.Errorf("%s: unbalanced brackets (%+d):\n%s", c.name, d, code)
		}
	}
}

// Every model reads back as one line, and it is clipped to the row budget.
func TestMLCorpusPreviews(t *testing.T) {
	for _, c := range mlCorpus {
		p := mlPreview(mltree(c.arch))
		if p == "" {
			t.Errorf("%s: empty preview", c.name)
		}
		if r := []rune(p); len(r) > mlPreviewMax {
			t.Errorf("%s: preview is %d runes, want ≤ %d", c.name, len(r), mlPreviewMax)
		}
		if strings.Contains(p, "\n") {
			t.Errorf("%s: preview spans lines: %q", c.name, p)
		}
	}
}

func mlLeafCount(it *item) int {
	if len(it.children) == 0 {
		return 1
	}
	n := 0
	for _, c := range it.children {
		n += mlLeafCount(c)
	}
	return n
}
