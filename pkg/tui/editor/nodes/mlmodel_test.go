package nodes

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

func mlRunes(v *mlView, h *fakeHost, n *fakeNode, s string) {
	t := mlView{}
	if v == nil {
		v = &t
	}
	for _, r := range s {
		if r == ' ' {
			v.Key(h, n, tea.KeyMsg{Type: tea.KeySpace})
		} else {
			v.Key(h, n, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

// TestMLPickSeedsFamily: a fresh node opens on the picker; filtering to the
// transformer and selecting it seeds that family's default fields, and Leave
// persists the definition.
func TestMLPickSeedsFamily(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "m1", typ: database.TypeMLModel, text: "my-gpt"}

	v := mlView{}
	if !v.Enter(h, n) {
		t.Fatal("card must open")
	}
	if !mlStateOf(h, "m1").picking {
		t.Fatal("no arch yet → the picker face")
	}
	mlRunes(&v, h, n, "transf")
	v.Key(h, n, tea.KeyMsg{Type: tea.KeyEnter})
	if st := mlStateOf(h, "m1"); st.picking || st.d.Arch != "transformer" {
		t.Fatalf("select must land on transformer: %+v", st.d)
	}
	v.Leave(h, n)

	d := mlLoad(h, "m1")
	if d.Arch != "transformer" || mlStr(d, "d_model") != "512" || mlStr(d, "vocab_size") != "32000" {
		t.Fatalf("persisted definition = %+v", d)
	}
}

// TestMLFieldEdit: the field editor round-trips — select a field, retype its
// value, Leave flushes it to node_output.
func TestMLFieldEdit(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "m1", typ: database.TypeMLModel, text: "my-gpt"}
	fam, _ := mlFamilyOf("transformer")
	mlSave(h, "m1", mlSeed(mlData{}, fam))

	v := mlView{}
	if !v.Enter(h, n) {
		t.Fatal("card must open")
	}
	if mlStateOf(h, "m1").picking {
		t.Fatal("arch present → the field face, not the picker")
	}
	// d_model is the first field; clear "512" and type "768"
	for i := 0; i < 3; i++ {
		v.Key(h, n, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	mlRunes(&v, h, n, "768")
	v.Leave(h, n)

	if got := mlStr(mlLoad(h, "m1"), "d_model"); got != "768" {
		t.Fatalf("d_model = %q, want 768", got)
	}
}

// TestMLRepickKeepsSharedKeys: alt+a re-picks the family; values whose keys
// exist in both families survive the switch (mlp→vae keeps input/hidden).
func TestMLRepickKeepsSharedKeys(t *testing.T) {
	mlp, _ := mlFamilyOf("mlp")
	d := mlSeed(mlData{}, mlp)
	for i := range d.Fields {
		if d.Fields[i].K == "hidden" {
			d.Fields[i].V = "1024"
		}
	}
	vae, _ := mlFamilyOf("vae")
	d = mlSeed(d, vae)
	if d.Arch != "vae" || mlStr(d, "hidden") != "1024" {
		t.Fatalf("switch must keep shared keys: %+v", d)
	}
	if mlStr(d, "latent") != "32" {
		t.Fatalf("new keys must seed defaults: %+v", d)
	}
}

// TestMLParamEstimates: each family's alt+r estimator on its defaults.
func TestMLParamEstimates(t *testing.T) {
	for _, tc := range []struct {
		arch, want string
	}{
		{"transformer", "35.3M"}, // 6·(4·512² + 2·512·2048) + 32000·512
		{"dlgn", "768K"},         // 6 layers · 8000 gates · 16 logits
		{"sam", "641M"},          // vit_h checkpoint
		{"rnn", "917.5K"},        // lstm: 4·256·(128+256) + 4·256·2·256
	} {
		fam, ok := mlFamilyOf(tc.arch)
		if !ok || fam.params == nil {
			t.Fatalf("%s must exist with an estimator", tc.arch)
		}
		if got := mlHuman(fam.params(mlSeed(mlData{}, fam))); got != tc.want {
			t.Fatalf("%s estimate = %s, want %s", tc.arch, got, tc.want)
		}
	}
}

// TestMLRunFlashes: alt+r is ephemeral — a flash, never a persisted output.
func TestMLRunFlashes(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "m1", typ: database.TypeMLModel, text: "my-gpt"}

	runMLModel(h, n)
	if !strings.Contains(h.flash, "pick an architecture") {
		t.Fatalf("no arch → nudge flash, got %q", h.flash)
	}

	fam, _ := mlFamilyOf("transformer")
	mlSave(h, "m1", mlSeed(mlData{}, fam))
	runMLModel(h, n)
	if !strings.Contains(h.flash, "35.3M") {
		t.Fatalf("estimate flash = %q", h.flash)
	}
	// diffusion carries no estimator — the flash summarizes instead
	diff, _ := mlFamilyOf("diffusion")
	mlSave(h, "m1", mlSeed(mlData{}, diff))
	runMLModel(h, n)
	if !strings.Contains(h.flash, "no param estimate") {
		t.Fatalf("no-estimator flash = %q", h.flash)
	}
}

// TestMLToContext: agents read <mlmodel arch="…"> with field: value lines.
func TestMLToContext(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "m1", typ: database.TypeMLModel, text: "my-gpt"}
	fam, _ := mlFamilyOf("gan")
	mlSave(h, "m1", mlSeed(mlData{}, fam))

	tag, attrs, body := mlToContext(h, n)
	if tag != "mlmodel" || attrs != `arch="gan"` {
		t.Fatalf("context tag/attrs = %q %q", tag, attrs)
	}
	if !strings.Contains(body, "latent_dim: 100") || !strings.Contains(body, "g_hidden: 256") {
		t.Fatalf("context body = %q", body)
	}
}

// TestMLRenderChip: the inline row wears a dim {arch} chip once the family is
// picked, and {model?} before.
func TestMLRenderChip(t *testing.T) {
	h := newFakeHost(t)
	n := &fakeNode{uuid: "m1", typ: database.TypeMLModel, text: "my-gpt"}
	if got := mlRender(h, n); !strings.Contains(got, "{model?}") {
		t.Fatalf("unpicked render = %q", got)
	}
	fam, _ := mlFamilyOf("rnn")
	mlSave(h, "m1", mlSeed(mlData{}, fam))
	if got := mlRender(h, n); !strings.Contains(got, "{rnn}") || !strings.Contains(got, "my-gpt") {
		t.Fatalf("picked render = %q", got)
	}
}
