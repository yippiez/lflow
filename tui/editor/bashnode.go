package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/tui/database"
)

// The Bash node is a shell command composed AS an outline — the same idea as the
// Math node, applied to shell: a "$" row whose CHILDREN are its parts, so a long
// pipeline is written as a readable tree instead of one wide line.
//
//	$ rg                     →  rg --hidden -n "func .*Msg" tui/editor | wc -l
//	  ├─ --hidden -n
//	  ├─ "func .*Msg"
//	  ├─ tui/editor
//	  ╰─ $ |
//	       ╰─ wc -l
//
// Composition is literal and predictable, one rule per node text:
//
//   - a JOIN operator (| |& && || ;) joins its children with that operator
//   - a WRAPPER ($() or ()) puts its children inside the wrapper
//   - empty text is a plain container: its children join with a space
//   - anything else is a head: its own text, then its children, space-separated
//
// A COMPLETED node (and its subtree) is skipped, which is how you comment a flag
// out of a command without deleting it. Chips resolve on the way through, so a
// path chip runs as its full path and displays as its compact form.
//
// alt+r composes THIS node's subtree and runs it through the shared run machinery
// (runShell), so running a sub-node runs just that part of the command. The row
// carries a dim preview of what would run; alt+e opens the output.

func init() {
	registerType(nodeType{
		key:             database.TypeBash,
		label:           "Bash",
		inlineEditable:  true,
		continueOnEnter: true,
		runInTail:       true,
		cliDeps:         []string{"bash"},
		glyph: func(*item) (string, string) {
			return "$", cRed
		},
		spanColor:    bashSpanColor,
		bodyTail:     bashBodyTail,
		run:          runBashNode,
		view:         runOutView{},
		flashActions: bashFlashActions,
	})
}

// bashJoins are the node texts that JOIN their children instead of heading them.
var bashJoins = map[string]bool{"|": true, "|&": true, "&&": true, "||": true, ";": true}

// bashWraps are the node texts that WRAP their composed children.
var bashWraps = map[string][2]string{
	"$()": {"$(", ")"},
	"()":  {"(", ")"},
}

// bashOpTokens are the shell operators tinted yellow wherever they appear in a
// bash node's text — the join operators plus redirections, so the syntax reads at
// a glance. Longest first: the scan takes the first match.
var bashOpTokens = []string{"2>&1", "&&", "||", "|&", ">>", "<<", "$(", "|", ";", ">", "<", ")"}

// bashCompose flattens a bash node's subtree into the one command line it stands
// for. text transforms each node's stored text on the way through — expandAnchors
// for a run (chips become their real values), displayAnchors for the row preview.
// Pure and recursive, so the same routine feeds the run, the preview and context.
func bashCompose(it *item, text func(string) string) string {
	if it == nil || it.completedAt > 0 {
		return "" // a completed node is commented out, subtree and all
	}
	self := strings.TrimSpace(text(it.name))
	var kids []string
	for _, c := range it.children {
		if s := bashCompose(c, text); s != "" {
			kids = append(kids, s)
		}
	}
	if len(kids) == 0 {
		return self
	}
	switch {
	case bashJoins[self]:
		return strings.Join(kids, " "+self+" ")
	case len(bashWraps[self][0]) > 0:
		w := bashWraps[self]
		return w[0] + strings.Join(kids, " ") + w[1]
	case self == "":
		return strings.Join(kids, " ")
	default:
		return self + " " + strings.Join(kids, " ")
	}
}

// bashCommand is the runnable command a node stands for: chips expanded to their
// real values (a path chip becomes its absolute path), ready for `bash -c`.
func (m *Model) bashCommand(it *item) string {
	return bashCompose(it, func(s string) string { return expandAnchors(s, m.chips) })
}

// bashPreview is the human form of the same command — chips in their compact
// display form — used for the row's dim tail and structured context.
func bashPreview(it *item, chips map[string]database.Chip) string {
	return bashCompose(it, func(s string) string { return displayAnchors(s, chips) })
}

// bashGlyph marks the row with the same red "$" the bash chip wears — the two
// shell surfaces are one thing, and a Bash node is only the tree form of it. The
// glyph never changes: not on a fold, not while the run's terminal is open.
func bashGlyph(*item) (string, string) { return glyphBashPrompt, cRed }

// bashBodyTail is the row's "→" section, and it is the SAME surface a bash chip's
// tail is — a bash node is only the tree form of that chip, so the two must read
// alike (see runTails):
//
//   - with a run in hand it hangs that run's headline: the newest line while the
//     command is in flight (shimmering, the way the running chip's cell does), the
//     first line once it has settled. The row IS the live surface — a running node
//     claims no band beneath it, exactly like the chip.
//   - with no run it falls back to the composed command, the same service math's
//     linear preview does: the tree stays readable while the row still says
//     exactly what alt+r would run. A leaf's text IS its command, so a leaf with
//     nothing to show gets no tail at all.
func bashBodyTail(it *item, chips map[string]database.Chip) string {
	if it == nil {
		return ""
	}
	if t, ok := runTails[it.uuid]; ok {
		// the tail belongs to the command that was RUN. If the node's command
		// changed since (the composed form differs), the old result is stale —
		// the tail reverts to the new composed command below instead.
		if t.cmd == "" || t.cmd == bashCompose(it, func(s string) string { return expandAnchors(s, chips) }) {
			// running but nothing printed yet: the row still shimmers — a silent
			// command (`sleep 30`) is alive, not idle, so it wears the same
			// shimmering "running…" the chip's cell does instead of the dim preview
			if t.running && t.text == "" {
				return cReset + shimmerLabel("→ running…") + cReset
			}
			if t.text != "" {
				if t.running {
					return cReset + shimmerLabel("→ "+t.text) + cReset
				}
				return cDim + "→ " + t.text + cReset
			}
		}
	}
	if len(it.children) == 0 {
		return ""
	}
	p := bashPreview(it, chips)
	if p == "" || p == strings.TrimSpace(displayAnchors(it.name, chips)) {
		return ""
	}
	return cDim + "→ " + clipStr(p, 96) + cReset
}

// bashSpanColor tints the shell operators in a bash node's text yellow, so an
// operator row (a bare "|") and an inline redirect read the same way.
func bashSpanColor(it *item, runes []rune) map[int]string {
	if len(runes) == 0 {
		return nil
	}
	out := make(map[int]string)
	for i := 0; i < len(runes); {
		n := matchTok(bashOpTokens, runes, i)
		if n == 0 {
			i++
			continue
		}
		for k := i; k < i+n; k++ {
			out[k] = cYellow
		}
		i += n
	}
	return out
}

// runBashNode (alt+r) composes this node's subtree and runs it, streaming into
// the node's own run band. A second alt+r (or alt+x) stops it — runShell owns
// that toggle, exactly as a bash chip does.
func runBashNode(m *Model, it *item) tea.Cmd {
	cmd := m.bashCommand(it)
	if strings.TrimSpace(cmd) == "" {
		m.errorFlash("bash: nothing to run")
		return nil
	}
	return runShell(m, it.uuid, cmd)
}

// bashFlashActions names the alt+r action "run $" in the flash bar, matching the
// verb a bash chip uses.
func bashFlashActions(m *Model, it *item) []flashAction {
	return []flashAction{{verb: "run $", color: cGreen, do: runBashNode}}
}
