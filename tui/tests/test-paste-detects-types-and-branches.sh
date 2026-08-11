#!/usr/bin/env bash
# test-paste-detects-types-and-branches.sh
#
# The paste parser, end to end through a real terminal paste.
#
# Pasting is how most text enters an outliner, and what arrives is whatever the
# other program put on the clipboard: markdown markers, checkboxes, a fence,
# indentation of no fixed step. The parser (tui/editor/pasteparse.go) reads that
# plain text back into TYPED NODES and BRANCHES — there is no private clipboard
# format to lean on, by design, so this must work on the text alone.
#
# What this asserts, on one paste:
#   1. "## Sprint" becomes a heading (glyph "2"), not a row spelling out hashes.
#   2. "- [ ]" / "- [x]" become todo rows (□ open, ■ done) with the boxes gone.
#   3. Indentation becomes real branches — the child hangs under its parent on
#      the tree rail (├─ / ╰─), instead of a name that starts with spaces.
#   4. "> quoted" becomes a quote row and "$ cmd" a bash row ("$" glyph).
#   5. No marker text is left over anywhere, and nothing crashes.
#
# The bracketed-paste sequence is delivered as raw bytes through the tmux
# buffer, exactly as a real terminal paste does, so bubbletea sees k.Paste.

set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

setup; launch

# Start on a fresh empty row below a named node, which is where a paste lands
# after Enter.
type "notes"
wait_for "○ notes"
send Enter

# ESC[200~ … ESC[201~ wraps the payload: a heading, two checkboxes (one of them
# with an indented child), a quote and a shell command.
PASTE_SEQ="$(printf '\x1b[200~## Sprint\r\n- [x] parse it\r\n- [ ] test it\r\n    - the broken cases\r\n> ship on friday\r\n$ go test ./...\r\n\x1b[201~')"
tmux load-buffer - <<< "${PASTE_SEQ}"
tmux paste-buffer -t "${SESSION}"

# The last pasted row is the bash command: once it is on screen the whole paste
# has been processed.
wait_for "go test ./..." 5

pane="$(snapshot)"

# 1. the heading: the H2 glyph is the digit "2", and no "##" survives anywhere
assert_contains "2 Sprint"
assert_not_contains "## Sprint"

# 2. the todos: ■ for the ticked one, □ for the open one, boxes stripped
assert_contains "■ parse it"
assert_contains "□ test it"
assert_not_contains "[x]"
assert_not_contains "[ ]"

# 3. the branch: the child hangs on the tree rail under "test it", and its name
#    does not start with the spaces it was indented by
if ! printf '%s\n' "${pane}" | grep -qE '[├╰]─.*the broken cases'; then
    fail "indented line did not become a child branch: no tree rail before it"
fi

# 4. the quote and the command rows
assert_contains "ship on friday"
assert_contains "$ go test ./..."

# 5. no leftover markers from the paste
assert_not_contains "- ["
assert_not_contains "> ship"

# the node we pasted into is still there, above everything
assert_contains "○ notes"

assert_no_crash
pass
