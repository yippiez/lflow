package editor

// GROUP C — external process picker.
//
// SCAFFOLD ONLY: signatures + data structures + TODOs for review. Real logic
// panics so the package still builds.
//
// This one is the outlier. It does not live in the mode system and holds no
// persistent Model state: it suspends the inline TUI (tea.ExecProcess), runs an
// external fuzzy picker on /dev/tty, and hands the selection to an onPick
// callback that builds whatever tea.Msg the caller needs. The /file picker (fzf)
// is the only instance today; the wrapper mostly buys a single "is it installed?"
// check and one place to add future external pickers.
//
// Design decisions locked in review:
//   - onPick callback builds the msg — externalPicker carries NO splice context,
//     so no shared externalPickedMsg type (the /file caller keeps fzfPickedMsg)
//   - no stdin `input` field yet — fzf walks the cwd; add piping when a second
//     consumer actually needs it
//   - the missing-binary flash is set at the CALL SITE — run() returns nil when
//     unavailable and never touches Model, keeping this helper UI-agnostic

import (
	"bytes"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// externalPicker describes an external fuzzy-picker invocation. It is a plain
// value — construct one, call run, done. No Model coupling.
type externalPicker struct {
	bin    string   // executable to look up and run (e.g. "fzf")
	prompt string   // shown to the user (fzf --prompt)
	args   []string // any extra args before the prompt
}

// available reports whether the picker binary is on PATH (exec.LookPath). The
// caller uses this (or run's nil return) to decide whether to flash its own
// "fzf not found — install it" message; this helper sets no flash itself.
func (e externalPicker) available() bool {
	_, err := exec.LookPath(e.bin)
	return err == nil
}

// run suspends the inline UI and executes the picker, capturing stdout. onPick
// maps the selected line (trimmed) to the tea.Msg delivered back to Update, so
// each caller attaches its own splice context (uuid, caret, …) in its own msg
// type. Returns nil when the binary is missing — the caller flashes.
func (e externalPicker) run(onPick func(selection string) tea.Msg) tea.Cmd {
	if !e.available() {
		return nil
	}
	args := append([]string{}, e.args...)
	if e.prompt != "" {
		args = append(args, "--prompt", e.prompt)
	}
	c := exec.Command(e.bin, args...)
	var out bytes.Buffer
	c.Stdout = &out // the picker draws on /dev/tty and prints the selection to stdout
	return tea.ExecProcess(c, func(error) tea.Msg {
		return onPick(strings.TrimSpace(out.String()))
	})
}
