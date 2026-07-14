package nodes

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The mlmodel node: an ML model definition as an outline node — ONE node type
// carrying a FAMILY of architectures (rnn, transformer, gan, sam, dlgn, cnn,
// mlp, vae, diffusion). The node text is the model's name; the chosen
// architecture and its hyperparameter fields live in node_output JSON (local,
// decoupled from the row). alt+e opens the definition card: first the
// architecture picker (type to filter, enter selects and seeds that family's
// default fields), then the editable field list (up/down selects a field,
// typing appends to its value, alt+a re-picks the family keeping values whose
// keys survive the switch). alt+r flashes an order-of-magnitude parameter
// estimate computed from the fields — ephemeral, never persisted. Agents read
// the definition via <mlmodel arch="…"> with one "field: value" line per field.

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

// mlField is one hyperparameter of the definition — an ordered k/v pair, so a
// family's fields always read in their canonical order. Values stay strings:
// a field can hold an int, a cell kind ("lstm"), or a list ("1,2,4,8").
type mlField struct {
	K string `json:"k"`
	V string `json:"v"`
}

// mlData is the persisted definition (node_output JSON).
type mlData struct {
	Arch   string    `json:"arch,omitempty"`
	Fields []mlField `json:"fields,omitempty"`
}

// mlFamily is one architecture in the family: its picker entry, the default
// fields a fresh definition seeds, and the parameter estimator behind alt+r
// (nil → no estimate; the flash shows the definition summary instead).
type mlFamily struct {
	key, label, desc string
	defaults         []mlField
	params           func(d mlData) int64
}

// mlFamilies is the architecture family — the picker list, in display order.
// Estimates are ORDER-OF-MAGNITUDE weight counts (biases/norms ignored),
// there to sanity-check a definition, not to plan a training run.
var mlFamilies = []mlFamily{
	{
		key: "rnn", label: "RNN", desc: "recurrent net over sequences (vanilla/LSTM/GRU cells)",
		defaults: []mlField{{"cell", "lstm"}, {"input_size", "128"}, {"hidden_size", "256"}, {"num_layers", "2"}, {"bidirectional", "false"}},
		params: func(d mlData) int64 {
			mult := int64(1)
			switch mlStr(d, "cell") {
			case "lstm":
				mult = 4
			case "gru":
				mult = 3
			}
			in, h, layers := mlInt(d, "input_size"), mlInt(d, "hidden_size"), mlInt(d, "num_layers")
			if h == 0 || layers == 0 {
				return 0
			}
			p := mult * h * (in + h) // first layer
			if layers > 1 {
				p += (layers - 1) * mult * h * 2 * h // stacked layers take the prior layer's h
			}
			if mlStr(d, "bidirectional") == "true" {
				p *= 2
			}
			return p
		},
	},
	{
		key: "transformer", label: "Transformer", desc: "attention encoder/decoder stack (GPT/BERT shape)",
		defaults: []mlField{{"d_model", "512"}, {"n_heads", "8"}, {"n_layers", "6"}, {"d_ff", "2048"}, {"vocab_size", "32000"}, {"context_len", "1024"}},
		params: func(d mlData) int64 {
			dm, dff, layers, vocab := mlInt(d, "d_model"), mlInt(d, "d_ff"), mlInt(d, "n_layers"), mlInt(d, "vocab_size")
			if dm == 0 || layers == 0 {
				return 0
			}
			// per block: QKVO (4·d²) + the FFN pair (2·d·d_ff); plus tied embeddings
			return layers*(4*dm*dm+2*dm*dff) + vocab*dm
		},
	},
	{
		key: "gan", label: "GAN", desc: "generator vs discriminator, adversarially trained",
		defaults: []mlField{{"latent_dim", "100"}, {"g_hidden", "256"}, {"g_layers", "4"}, {"d_hidden", "256"}, {"d_layers", "4"}, {"image_size", "64"}},
		params: func(d mlData) int64 {
			g := mlInt(d, "g_layers") * mlInt(d, "g_hidden") * mlInt(d, "g_hidden")
			dd := mlInt(d, "d_layers") * mlInt(d, "d_hidden") * mlInt(d, "d_hidden")
			return g + dd
		},
	},
	{
		key: "sam", label: "SAM", desc: "Segment Anything: ViT image encoder + prompt/mask decoders",
		defaults: []mlField{{"variant", "vit_h"}, {"image_size", "1024"}, {"patch_size", "16"}, {"points_per_side", "32"}},
		params: func(d mlData) int64 {
			// published checkpoint sizes — the variant IS the parameter count
			switch mlStr(d, "variant") {
			case "vit_b":
				return 94_000_000
			case "vit_l":
				return 312_000_000
			case "vit_h":
				return 641_000_000
			}
			return 0
		},
	},
	{
		key: "dlgn", label: "Diff Logic Gate Net", desc: "differentiable logic gate network: layers of learned binary gates",
		defaults: []mlField{{"num_layers", "6"}, {"gates_per_layer", "8000"}, {"gate_set", "16"}, {"tau", "10"}},
		params: func(d mlData) int64 {
			// one softmax distribution over the gate set per gate
			return mlInt(d, "num_layers") * mlInt(d, "gates_per_layer") * mlInt(d, "gate_set")
		},
	},
	{
		key: "cnn", label: "CNN", desc: "convolutional stack + classifier head",
		defaults: []mlField{{"conv_layers", "4"}, {"base_channels", "32"}, {"kernel", "3"}, {"fc_hidden", "256"}, {"num_classes", "10"}},
		params: func(d mlData) int64 {
			k, c := mlInt(d, "kernel"), mlInt(d, "base_channels")
			if k == 0 || c == 0 {
				return 0
			}
			var p int64
			prev := int64(3) // RGB in
			for i := int64(0); i < mlInt(d, "conv_layers"); i++ {
				p += k * k * prev * c
				prev, c = c, c*2 // channels double per stage
			}
			fc := mlInt(d, "fc_hidden")
			return p + prev*fc + fc*mlInt(d, "num_classes")
		},
	},
	{
		key: "mlp", label: "MLP", desc: "plain fully-connected stack",
		defaults: []mlField{{"input", "784"}, {"hidden", "256"}, {"depth", "3"}, {"output", "10"}},
		params: func(d mlData) int64 {
			in, h, depth, out := mlInt(d, "input"), mlInt(d, "hidden"), mlInt(d, "depth"), mlInt(d, "output")
			if h == 0 || depth == 0 {
				return 0
			}
			p := in*h + h*out
			if depth > 2 {
				p += (depth - 2) * h * h
			}
			return p
		},
	},
	{
		key: "vae", label: "VAE", desc: "variational autoencoder: encoder → latent → decoder",
		defaults: []mlField{{"input", "784"}, {"hidden", "512"}, {"latent", "32"}},
		params: func(d mlData) int64 {
			in, h, z := mlInt(d, "input"), mlInt(d, "hidden"), mlInt(d, "latent")
			// encoder (in→h→2z for μ,σ) + decoder (z→h→in)
			return in*h + h*2*z + z*h + h*in
		},
	},
	{
		key: "diffusion", label: "Diffusion", desc: "denoising U-Net over a noise schedule",
		defaults: []mlField{{"unet_base", "64"}, {"channel_mults", "1,2,4,8"}, {"timesteps", "1000"}, {"attention_res", "16"}},
		// no honest closed-form for a U-Net — alt+r shows the definition summary
	},
}

func mlFamilyOf(key string) (mlFamily, bool) {
	for _, f := range mlFamilies {
		if f.key == key {
			return f, true
		}
	}
	return mlFamily{}, false
}

// mlFiltered narrows the family list by a fuzzy filter (case-insensitive,
// matched against key+label).
func mlFiltered(filter string) []mlFamily {
	if filter == "" {
		return mlFamilies
	}
	needle := strings.ToLower(filter)
	var out []mlFamily
	for _, f := range mlFamilies {
		if editor.NodeFuzzyMatch(strings.ToLower(f.key+" "+f.label), needle) {
			out = append(out, f)
		}
	}
	return out
}

func mlStr(d mlData, k string) string {
	for _, f := range d.Fields {
		if f.K == k {
			return strings.TrimSpace(f.V)
		}
	}
	return ""
}

// mlInt reads a field as a non-negative integer; anything else reads 0, which
// the estimators treat as "incomplete".
func mlInt(d mlData, k string) int64 {
	v, err := strconv.ParseInt(mlStr(d, k), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
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

// ── persistence + view state ────────────────────────────────────────────────

// mlState is the in-memory card state (NodeStore, key "mlmodel"): the working
// copy of the definition while the card is open, flushed on Leave.
type mlState struct {
	open    bool
	d       mlData // working copy; persisted on Leave
	picking bool   // the architecture-picker face
	filter  string // picker fuzzy filter
	pickSel int    // picker selection (index into the filtered list)
	sel     int    // selected field row on the definition face
}

func mlStateOf(h editor.NodeHost, uuid string) *mlState {
	s := h.NodeStore(uuid)
	st, _ := s["mlmodel"].(*mlState)
	if st == nil {
		st = &mlState{}
		s["mlmodel"] = st
	}
	return st
}

func mlLoad(h editor.NodeHost, uuid string) mlData {
	var d mlData
	db := h.NodeDB()
	if db == nil {
		return d
	}
	if raw, err := database.LoadNodeOutput(db, uuid); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	return d
}

func mlSave(h editor.NodeHost, uuid string, d mlData) {
	db := h.NodeDB()
	if db == nil {
		return
	}
	if raw, err := json.Marshal(d); err == nil {
		_ = database.SaveNodeOutput(db, uuid, string(raw))
	}
}

// mlSeed switches the definition to a family: its defaults, carrying over the
// old values whose keys survive the switch (mlp→vae keeps input/hidden).
func mlSeed(old mlData, fam mlFamily) mlData {
	keep := map[string]string{}
	for _, f := range old.Fields {
		keep[f.K] = f.V
	}
	fields := make([]mlField, len(fam.defaults))
	copy(fields, fam.defaults)
	if old.Arch != "" {
		for i := range fields {
			if v, ok := keep[fields[i].K]; ok {
				fields[i].V = v
			}
		}
	}
	return mlData{Arch: fam.key, Fields: fields}
}

// ── inline row, alt+r, agent context ────────────────────────────────────────

// mlRender is the inline body: the model's name plus a dim {arch} chip — or
// {model?} while the architecture is still unpicked, nudging alt+e.
func mlRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	chip := "{model?}"
	if d := mlLoad(h, n.UUID()); d.Arch != "" {
		chip = "{" + d.Arch + "}"
	}
	return th.FG + n.Text() + th.Reset + " " + th.Dim + chip + th.Reset
}

// runMLModel (alt+r) flashes the family's parameter estimate — ephemeral, like
// every run output: never persisted, never synced.
func runMLModel(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	d := mlLoad(h, n.UUID())
	if st := mlStateOf(h, n.UUID()); st.open {
		d = st.d // prefer the live working copy while the card is open
	}
	if d.Arch == "" {
		h.NodeFlash("pick an architecture first · alt+e")
		return nil
	}
	fam, ok := mlFamilyOf(d.Arch)
	if !ok || fam.params == nil {
		h.NodeFlash(d.Arch + " · " + strconv.Itoa(len(d.Fields)) + " fields · no param estimate")
		return nil
	}
	p := fam.params(d)
	if p <= 0 {
		h.NodeFlash(d.Arch + " · fields incomplete, no estimate")
		return nil
	}
	h.NodeFlash(d.Arch + " ≈ " + mlHuman(p) + " params (order-of-magnitude)")
	return nil
}

// mlToContext renders the definition for an agent: <mlmodel arch="…"> with
// one "field: value" line per field as the body.
func mlToContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	d := mlLoad(h, n.UUID())
	attrs := ""
	if d.Arch != "" {
		attrs = `arch="` + d.Arch + `"`
	}
	lines := make([]string, 0, len(d.Fields))
	for _, f := range d.Fields {
		lines = append(lines, f.K+": "+f.V)
	}
	return "mlmodel", attrs, strings.Join(lines, "\n")
}

// ── the definition card (alt+e) ─────────────────────────────────────────────

// mlView is the alt+e definition card, two faces of its own: the architecture
// picker (until a family is chosen, or alt+a) and the field editor. Stateless
// per the view contract — per-node state in mlState (NodeStore).
type mlView struct{}

func (mlView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	st := mlStateOf(h, n.UUID())
	st.d = mlLoad(h, n.UUID())
	st.open, st.picking = true, st.d.Arch == ""
	st.filter, st.pickSel, st.sel = "", 0, 0
	return true
}

// Leave flushes the working definition back to node_output.
func (mlView) Leave(h editor.NodeHost, n editor.NodeRef) {
	st := mlStateOf(h, n.UUID())
	if st.open {
		mlSave(h, n.UUID(), st.d)
	}
	st.open, st.picking = false, false
}

func (mlView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	st := mlStateOf(h, n.UUID())
	if st.picking {
		if k := len(mlFiltered(st.filter)); k > 0 {
			return 1 + k
		}
		return 2 // header + "no match"
	}
	return 1 + len(st.d.Fields)
}

func (mlView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	st := mlStateOf(h, n.UUID())
	if k.String() == "alt+r" {
		return runMLModel(h, n), true
	}
	if st.picking {
		return mlPickerKey(st, k)
	}
	switch k.String() {
	case "alt+a": // re-pick the family
		st.picking, st.filter, st.pickSel = true, "", 0
		return nil, true
	case "up":
		if st.sel > 0 {
			st.sel--
		}
	case "down":
		if st.sel < len(st.d.Fields)-1 {
			st.sel++
		}
	case "enter", "tab":
		if len(st.d.Fields) > 0 {
			st.sel = (st.sel + 1) % len(st.d.Fields)
		}
	case "backspace":
		if st.sel < len(st.d.Fields) {
			if v := []rune(st.d.Fields[st.sel].V); len(v) > 0 {
				st.d.Fields[st.sel].V = string(v[:len(v)-1])
			}
		}
	default:
		s := ""
		switch {
		case k.Type == tea.KeySpace && !k.Alt:
			s = " "
		case k.Type == tea.KeyRunes && !k.Alt:
			s = string(k.Runes)
		default:
			return nil, false // esc, alt+e, ctrl+c … → central
		}
		if st.sel < len(st.d.Fields) {
			st.d.Fields[st.sel].V += s
		}
	}
	return nil, true
}

// mlPickerKey drives the architecture-picker face: type to filter, up/down to
// move, enter to select (seeding the family's default fields).
func mlPickerKey(st *mlState, k tea.KeyMsg) (tea.Cmd, bool) {
	list := mlFiltered(st.filter)
	switch k.String() {
	case "up":
		if st.pickSel > 0 {
			st.pickSel--
		}
	case "down":
		if st.pickSel < len(list)-1 {
			st.pickSel++
		}
	case "backspace":
		if r := []rune(st.filter); len(r) > 0 {
			st.filter, st.pickSel = string(r[:len(r)-1]), 0
		}
	case "enter":
		if st.pickSel < len(list) {
			st.d = mlSeed(st.d, list[st.pickSel])
			st.picking, st.sel = false, 0
		}
	default:
		switch {
		case k.Type == tea.KeySpace && !k.Alt:
			st.filter, st.pickSel = st.filter+" ", 0
		case k.Type == tea.KeyRunes && !k.Alt:
			st.filter, st.pickSel = st.filter+string(k.Runes), 0
		default:
			return nil, false
		}
	}
	return nil, true
}

func (mlView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	th := editor.NodeTheme()
	st := mlStateOf(h, n.UUID())
	var lines []string
	if st.picking {
		head := "architecture — type to filter, enter selects"
		if st.filter != "" {
			head = "architecture ⌕ " + st.filter
		}
		lines = append(lines, rail+th.Dim+head+th.Reset)
		list := mlFiltered(st.filter)
		for i, fam := range list {
			marker, style := "  ", th.FG
			if i == st.pickSel {
				marker, style = "▸ ", th.Accent
			}
			lines = append(lines, rail+style+marker+fam.label+th.Reset+th.Dim+" — "+fam.desc+th.Reset)
		}
		if len(list) == 0 {
			lines = append(lines, rail+th.Dim+"  no match"+th.Reset)
		}
	} else {
		label, desc := st.d.Arch, "unknown architecture"
		if fam, ok := mlFamilyOf(st.d.Arch); ok {
			label, desc = fam.label, fam.desc
		}
		lines = append(lines, rail+th.Cyan+label+th.Reset+th.Dim+" — "+desc+" · alt+a re-picks · alt+r estimates"+th.Reset)
		keyW := 0
		for _, f := range st.d.Fields {
			if len(f.K) > keyW {
				keyW = len(f.K)
			}
		}
		for i, f := range st.d.Fields {
			pad := strings.Repeat(" ", keyW-len(f.K)+2)
			keyStyle, caret := th.Dim, ""
			if i == st.sel {
				keyStyle = th.Accent
				if focused {
					caret = th.Accent + "▌" + th.Reset
				}
			}
			lines = append(lines, rail+"  "+keyStyle+f.K+th.Reset+pad+th.FG+f.V+th.Reset+caret)
		}
	}
	for i := range lines {
		lines[i] = editor.NodeClip(lines[i], width)
	}
	return editor.NodeWindowBands(lines, scroll, winH)
}
