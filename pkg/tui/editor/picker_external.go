package editor

// GROUP C — external process picker.
//
// SCAFFOLD ONLY: signatures + data structures + TODOs for review. Bodies panic so
// the package still builds.
//
// This one is the outlier. It does not live in the mode system and holds no
// persistent Model state: it suspends the inline TUI (tea.ExecProcess), runs an
// external fuzzy picker on /dev/tty, and returns the selection as a tea.Msg. The
// /file picker (fzf) is the only instance today; a common wrapper mostly buys a
// single "is it installed?" check and one place to add future external pickers.

import (
	tea "github.com/charmbracelet/bubbletea"
)

// externalPicker describes an external fuzzy-picker invocation. It is a plain
// value — construct one, call run, done.
type externalPicker struct {
	bin    string   // executable to look up and run (e.g. "fzf")
	prompt string   // shown to the user (fzf --prompt)
	args   []string // any extra args beyond the prompt
	// TODO: an optional stdin feed. fzf currently walks the cwd itself, but a tag
	// or node external picker would pipe candidates in — add `input []string` or an
	// io.Reader when a second consumer appears. Don't build it speculatively.
}

// externalPickedMsg carries the chosen line back into Update, plus the caret
// context captured when the picker opened so the result can be spliced at the
// right spot. Generalizes the current fzfPickedMsg.
type externalPickedMsg struct {
	value string // the selected line (trimmed)

	// splice context — where to insert the result when the picker returns.
	// TODO: fzfPickedMsg carries uuid+caret today. Keep those, or pass an opaque
	// `ctx any` / a callback so non-path consumers aren't forced into path fields.
	uuid  string
	caret int
}

// available reports whether the picker binary is on PATH (exec.LookPath).
// TODO: on false, callers flash "fzf not found — install it"; decide if that
// message lives here or at the call site.
func (e externalPicker) available() bool {
	panic("TODO: implement externalPicker.available")
}

// run suspends the inline UI and executes the picker. onPick maps the selected
// line to the tea.Msg delivered back to Update (letting each caller build its own
// externalPickedMsg with the right splice context). Returns nil (and the caller
// flashes) when the binary is missing.
func (e externalPicker) run(onPick func(selection string) tea.Msg) tea.Cmd {
	panic("TODO: implement externalPicker.run")
}
