#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Repro: after typing two notes and returning to the outline, the pane used to
# show BOTH a dashed '╌╌ temp ╌╌' rule AND the status bar — two dividers
# between the main notes and the Temporary Domain panel.
#
# Fix (b706044): the dashed rule is gone; the status bar IS the single divider.
# Layout is: notes → status bar → temp panel (◌ nodes).
#
# Assertions:
#   1. The pane must NOT contain '╌╌' (the removed dashed rule).
#   2. The pane MUST contain '◌' (the always-visible Temporary Domain panel).
#   3. The status bar line (matching '· 2/2') appears exactly once —
#      confirming there is only one divider element between notes and the temp panel.

setup; launch

# Build two notes exactly as the repro steps specify.
type "note one"
send Enter
type "note two"

# Wait until both notes are visible.
wait_for "○ note two"
assert_contains "○ note one"

# The Temporary Domain panel must be visible (ensureTempTree guarantees ≥1 node).
assert_contains "◌"

# Core regression: the dashed '╌╌' temp rule must NOT appear anywhere in the pane.
assert_not_contains "╌╌"

# The status bar (which is now the sole divider) must be present.
# With two nodes the bar shows "· 2/2".
assert_contains "· 2/2"

# Verify the bar appears exactly once — there must not be a duplicate divider line.
pane="$(snapshot)"
bar_count="$(printf '%s\n' "${pane}" | grep -c '· [0-9]\+/[0-9]\+' || true)"
if (( bar_count != 1 )); then
    fail "expected exactly 1 status-bar line (the divider), got ${bar_count}"
fi

# The status bar line must appear BETWEEN the notes and the temp panel.
# Find the line numbers for the last note, the status bar, and the first ◌ node.
note_line="$(printf '%s\n' "${pane}" | grep -n '○ note two' | head -1 | cut -d: -f1)"
bar_line="$(printf '%s\n' "${pane}"  | grep -n '· 2/2'       | head -1 | cut -d: -f1)"
temp_line="$(printf '%s\n' "${pane}" | grep -n '◌'            | head -1 | cut -d: -f1)"

if ! (( note_line < bar_line && bar_line < temp_line )); then
    fail "layout wrong: note_two=${note_line} bar=${bar_line} temp_node=${temp_line}; expected note < bar < temp"
fi

assert_no_crash
pass
