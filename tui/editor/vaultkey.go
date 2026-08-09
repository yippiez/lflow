package editor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/crypto"
)

// The key prompt (modeVaultKey) — the page that stands between a sealed row and
// its contents. It is a full page rather than a band under the row for two
// reasons: a password typed inline would be typed INTO the outline, one
// keystroke away from being a node's text; and the page can say plainly what
// this particular vault is going to ask for before you start answering.
//
// It serves three occasions with one surface:
//
//	create — a node was just typed encrypted: pick the factors that will lock it
//	unlock — a sealed vault: answer the factors it was locked with
//	rekey  — an open vault: choose different factors and re-seal
//	search — an Encrypted Query: one key, tried against every sealed vault
//
// Argon2id runs synchronously on enter. At the interactive profile that is
// around a tenth of a second — one dropped frame, no spinner needed — and doing
// it on a worker would mean holding the user's password in a message queue.

type vaultKeyMode int

const (
	vaultKeyCreate vaultKeyMode = iota
	vaultKeyUnlock
	vaultKeyRekey
	vaultKeySearch
)

// vaultField is one row of the prompt.
type vaultField struct {
	kind    crypto.Kind
	label   string
	confirm bool // the "again" row that guards a typo in a new password
	toggle  bool // the token switch (create/rekey) rather than a text field
}

// vaultKeyPrompt is the page's whole state. The typed password lives here and
// nowhere else, and closePrompt wipes it.
type vaultKeyPrompt struct {
	uuid    string
	mode    vaultKeyMode
	fields  []vaultField
	sel     int
	pass    textField
	confirm textField
	keyfile textField
	token   bool
	slot    int
	reveal  bool
	err     string
	// factors is what a sealed envelope demands (unlock only).
	factors []crypto.Factor
}

// openVaultKey puts the prompt up for a node.
func (m *Model) openVaultKey(it *item, mode vaultKeyMode) {
	p := vaultKeyPrompt{uuid: it.uuid, mode: mode, slot: 2}
	switch mode {
	case vaultKeyUnlock:
		env, err := m.vaultEnvelope(it.uuid)
		if err != nil || env == nil {
			m.errorFlash("vault: nothing sealed here yet")
			return
		}
		p.factors = env.Factors()
		for _, f := range p.factors {
			switch f.Kind {
			case crypto.KindPassword:
				p.fields = append(p.fields, vaultField{kind: crypto.KindPassword, label: "password"})
			case crypto.KindKeyfile:
				label := "keyfile"
				if f.Hint != "" {
					label = "keyfile · was " + f.Hint
				}
				p.fields = append(p.fields, vaultField{kind: crypto.KindKeyfile, label: label})
			}
			// a token factor has no field: only the device can answer it, and it
			// is challenged when you press enter.
		}
	case vaultKeySearch:
		// a search does not know which factors the sealed vaults want, so it
		// offers the two a person can supply and tries them everywhere
		p.fields = []vaultField{
			{kind: crypto.KindPassword, label: "password"},
			{kind: crypto.KindKeyfile, label: "keyfile · optional path"},
		}
	default:
		p.fields = []vaultField{
			{kind: crypto.KindPassword, label: "password"},
			{kind: crypto.KindPassword, label: "again", confirm: true},
			{kind: crypto.KindKeyfile, label: "keyfile · optional path"},
		}
		if crypto.TokenAvailable() {
			p.fields = append(p.fields, vaultField{kind: crypto.KindToken, label: "hardware key", toggle: true})
		}
	}
	m.vaultKey = p
	m.mode = modeVaultKey
}

// closeVaultKey leaves the prompt and wipes what was typed into it.
func (m *Model) closeVaultKey() {
	m.vaultKey = vaultKeyPrompt{}
	m.mode = modeOutline
}

func (m *Model) handleVaultKeyKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.vaultKey
	switch k.String() {
	case "esc":
		m.closeVaultKey()
		return m, nil
	case "enter":
		return m, m.submitVaultKey()
	case "tab", "down":
		if len(p.fields) > 0 {
			p.sel = (p.sel + 1) % len(p.fields)
		}
		return m, nil
	case "shift+tab", "up":
		if len(p.fields) > 0 {
			p.sel = (p.sel + len(p.fields) - 1) % len(p.fields)
		}
		return m, nil
	case "alt+p":
		p.reveal = !p.reveal // peek at what you typed, for a long passphrase
		return m, nil
	}
	if p.sel >= len(p.fields) {
		return m, nil
	}
	f := p.fields[p.sel]
	if f.toggle {
		switch k.String() {
		case " ", "space":
			p.token = !p.token
		case "left":
			if p.slot > 1 {
				p.slot--
			}
		case "right":
			if p.slot < 2 {
				p.slot++
			}
		}
		if k.Type == tea.KeySpace {
			p.token = !p.token
		}
		return m, nil
	}
	target := p.fieldAt(p.sel)
	if target != nil && target.handleKey(k) {
		p.err = ""
	}
	return m, nil
}

// fieldAt is the editable buffer behind row i.
func (p *vaultKeyPrompt) fieldAt(i int) *textField {
	if i < 0 || i >= len(p.fields) {
		return nil
	}
	f := p.fields[i]
	switch {
	case f.confirm:
		return &p.confirm
	case f.kind == crypto.KindPassword:
		return &p.pass
	case f.kind == crypto.KindKeyfile:
		return &p.keyfile
	}
	return nil
}

// submitVaultKey answers the prompt.
func (m *Model) submitVaultKey() tea.Cmd {
	p := &m.vaultKey
	it := m.tree.byUUID[p.uuid]
	if it == nil {
		m.closeVaultKey()
		return nil
	}
	secrets, err := p.secrets()
	if err != nil {
		p.err = err.Error()
		return nil
	}
	defer secrets.Wipe()

	switch p.mode {
	case vaultKeyUnlock:
		env, err := m.vaultEnvelope(p.uuid)
		if err != nil || env == nil {
			p.err = "nothing sealed here yet"
			return nil
		}
		// Name the empty field before spending a tenth of a second in Argon2id
		// only to fail. "the keyfile is missing" and "that password is wrong" are
		// different problems and must not arrive as the same sentence.
		if missing := secrets.Missing(env.Factors()); len(missing) > 0 {
			p.err = "this vault also needs a " + vaultFactorSummary(missing)
			return nil
		}
		if err := m.unlockVault(it, env, secrets); err != nil {
			// a wrong key clears the password and leaves the prompt up: the
			// overwhelmingly likely next action is typing it again.
			p.pass = textField{}
			p.err = err.Error()
			return nil
		}
		m.closeVaultKey()
		m.flash = "vault unlocked · alt+e seals it again"
		return m.scheduleSync()
	case vaultKeySearch:
		m.searchVaults(it, secrets)
		m.closeVaultKey()
		return m.scheduleSync()
	case vaultKeyRekey:
		factors, err := p.newFactors()
		if err != nil {
			p.err = err.Error()
			return nil
		}
		if err := m.rekeyVault(it, factors, secrets); err != nil {
			p.err = err.Error()
			return nil
		}
		m.closeVaultKey()
		m.flash = "vault re-keyed · " + vaultFactorSummary(factors)
		return m.scheduleSync()
	default:
		factors, err := p.newFactors()
		if err != nil {
			p.err = err.Error()
			return nil
		}
		if err := m.createVault(it, factors, secrets); err != nil {
			p.err = err.Error()
			return nil
		}
		m.closeVaultKey()
		m.flash = "vault sealed · " + vaultFactorSummary(factors)
		return m.scheduleSync()
	}
}

// secrets gathers what was typed. The keyfile is read here rather than at
// derive time so a missing file is a prompt error, not a failed unlock that
// reads like a wrong password.
func (p *vaultKeyPrompt) secrets() (crypto.Secrets, error) {
	s := crypto.Secrets{}
	if p.pass.value != "" {
		s.Password = []byte(p.pass.value)
	}
	if path := strings.TrimSpace(p.keyfile.value); path != "" {
		raw, err := os.ReadFile(expandHome(path))
		if err != nil {
			return s, errNoKeyfile(path)
		}
		if len(raw) == 0 {
			return s, errNoKeyfile(path)
		}
		s.Keyfile = raw
	}
	return s, nil
}

// newFactors is the lock a create/rekey is building.
func (p *vaultKeyPrompt) newFactors() ([]crypto.Factor, error) {
	var out []crypto.Factor
	if p.pass.value != "" {
		if p.pass.value != p.confirm.value {
			return nil, errPassMismatch
		}
		out = append(out, crypto.Factor{Kind: crypto.KindPassword})
	}
	if path := strings.TrimSpace(p.keyfile.value); path != "" {
		out = append(out, crypto.Factor{Kind: crypto.KindKeyfile, Hint: filepath.Base(path)})
	}
	if p.token {
		f, err := crypto.NewTokenFactor(p.slot)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, errNoFactors
	}
	return out, nil
}

// expandHome resolves a leading ~ so a keyfile can be given the way it is
// spoken. Anything else is left exactly as typed.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// ── the page ────────────────────────────────────────────────────────────────

func (m *Model) viewVaultKey(maxLine int) []string {
	p := &m.vaultKey

	title, hint := " seal this node", "enter seals · esc cancel"
	switch p.mode {
	case vaultKeyUnlock:
		title = " unlock · " + vaultFactorSummary(p.factors)
		hint = "enter unlocks · alt+p peek · esc cancel"
	case vaultKeyRekey:
		title = " re-key this vault"
		hint = "enter re-seals · alt+p peek · esc cancel"
	case vaultKeySearch:
		title = " search your vaults · " + strconv.Itoa(m.sealedVaultCount()) + " still sealed"
		hint = "enter searches · alt+p peek · esc cancel"
	default:
		hint = "enter seals · alt+p peek · tab next · esc cancel"
	}

	lines := []string{clip(cDim+title+cReset, maxLine)}
	switch p.mode {
	case vaultKeyCreate, vaultKeyRekey:
		lines = append(lines, clip(cDim+" every factor you set is required to open it again"+cReset, maxLine))
	case vaultKeySearch:
		lines = append(lines, clip(cDim+" this key is tried against every sealed vault; the rest stay shut"+cReset, maxLine))
	}
	lines = append(lines, "")

	for i, f := range p.fields {
		label := cDim
		if i == p.sel {
			label = cAccent
		}
		var value string
		switch {
		case f.toggle:
			value = cDim + "off" + cReset
			if p.token {
				value = cGreen + "on" + cReset + cDim + " · slot " + strconv.Itoa(p.slot) + " · touch it when it blinks" + cReset
			} else if i == p.sel {
				value += cDim + " · space to arm" + cReset
			}
		case f.kind == crypto.KindKeyfile:
			value = cFG + fieldWithCaret(p.fieldAt(i), i == p.sel, false) + cReset
		default:
			value = cFG + fieldWithCaret(p.fieldAt(i), i == p.sel, !p.reveal) + cReset
		}
		lines = append(lines, clip(label+" "+padLabel(f.label)+cReset+value, maxLine))
	}

	lines = append(lines, "")
	if p.err != "" {
		lines = append(lines, clip(" "+cRed+p.err+cReset, maxLine))
	}
	lines = append(lines, clip(cDim+" "+hint+cReset, maxLine))
	lines = append(lines, "")
	lines = append(lines, clip(cDim+" "+crypto.Suite+cReset, maxLine))
	m.pageRows = len(lines) // no status bar here — the whole frame is main region
	return lines
}

// vaultLabelWidth keeps the value column aligned across rows whose labels carry
// a "· was <filename>" tail.
const vaultLabelWidth = 24

func padLabel(s string) string {
	if w := vaultLabelWidth - len([]rune(s)); w > 0 {
		return s + strings.Repeat(" ", w)
	}
	return s + " "
}

// fieldWithCaret renders a field, masked when it holds a password. The mask is
// one star per rune so the caret still lands where the typist expects — the
// alternative, a fixed-width blob, makes a backspace look like nothing
// happened. (A star rather than the usual round bullet, which this codebase's
// output style forbids in user-facing strings — see rules/no-status-emoji.yml.)
func fieldWithCaret(f *textField, focused, mask bool) string {
	if f == nil {
		return ""
	}
	value := f.value
	if mask {
		value = strings.Repeat("*", len([]rune(value)))
	}
	if !focused {
		return value
	}
	return withCaret(value, f.caret)
}

var (
	errPassMismatch = errVault("The two passwords do not match")
	errNoFactors    = errVault("Set a password, a keyfile or a hardware key — a vault needs at least one")
)

func errNoKeyfile(path string) error { return errVault("Cannot read the keyfile " + path) }

// errVault is a plain prompt-level error. These are shown in the page, not
// returned to a caller, so they carry no stack and no wrapping.
type errVault string

func (e errVault) Error() string { return string(e) }
