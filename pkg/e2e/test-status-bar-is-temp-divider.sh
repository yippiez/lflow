#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# The status bar is the SINGLE divider between the main notes and the Temporary Domain
# panel — there is no dashed '╌╌ temp ╌╌' rule. The empty compose line is hidden
# while focus is in the notes (new behavior); it appears once you enter the panel.
#
# Layout once focused in the Temporary Domain: notes (read-only) → status bar → ◌ panel.

setup; launch

type "note one"
send Enter
type "note two"
wait_for "○ note two"
assert_contains "○ note one"

# while in notes, the empty compose line is invisible
assert_not_contains "◌"
assert_not_contains "╌╌"

# enter the Temporary Domain — the compose line (◌) appears below the status bar
send Down
wait_for "◌"
assert_not_contains "╌╌" # the dashed rule must never appear

pane="$(snapshot)"
# exactly one status-bar line (the sole divider)
bar_count="$(printf '%s\n' "${pane}" | grep -c '· [0-9]\+/[0-9]\+' || true)"
if (( bar_count != 1 )); then
    fail "expected exactly 1 status-bar line (the divider), got ${bar_count}"
fi

# order: notes above the bar, bar above the temp panel
note_line="$(printf '%s\n' "${pane}" | grep -n '○ note two'        | head -1 | cut -d: -f1)"
bar_line="$(printf '%s\n' "${pane}"  | grep -n '· [0-9]\+/[0-9]\+' | head -1 | cut -d: -f1)"
temp_line="$(printf '%s\n' "${pane}" | grep -n '◌'                 | head -1 | cut -d: -f1)"
if ! (( note_line < bar_line && bar_line < temp_line )); then
    fail "layout wrong: note=${note_line} bar=${bar_line} temp=${temp_line}; expected note < bar < temp"
fi

assert_no_crash
pass
