#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Contract: the Table node is a READING of an ordinary subtree, and its two
# faces are the fold state.
#
#   sprint            /type Table
#   ├─ task           a first-level child IS a column (its text is the header)
#   │  ╰─ ship it     a column's child IS a cell (row 0 of that column)
#
# Expected:
#   - /type Table lands on the GRID face at once: a box with "task" on the
#     header row and "ship it" in the cell below it, and the column/cell rows
#     hidden (they are the grid now).
#   - alt+down opens the NODES face: the same nodes as an ordinary outline, no
#     grid.
#   - alt+up folds back to the grid — the toggle is the ordinary fold, no extra
#     key and no extra state.
#   - alt+e opens the grid editor (its hint line names the grid keys).
#   - nothing moved: the outline in the nodes face is exactly what was typed.

setup
launch

# --- build the subtree: sprint › task › ship it ----------------------------

type "sprint"
send Enter
wait_for "○ sprint"
wait_for "2/2"

type "task"
send Tab
wait_for "╰─"

send Enter
wait_for "3/3"
type "ship it"
send Tab
wait_for "○ ship it"

# back up onto the "sprint" row (ship it → task → sprint)
send Up
send Up
wait_for "○ sprint"

# --- convert it: /type → Table ---------------------------------------------

# "/" must land alone: a burst of runes arrives as ONE key message, and only a
# lone "/" opens the slash menu.
send End
type "/"
wait_for "/duplicate"
type "type"
wait_for "Set this node's type"
send Enter
wait_for "type:"
type "table"
send Enter

# --- the grid face ---------------------------------------------------------

wait_for "┌"
assert_contains "▦ sprint"
assert_contains "│ task"
assert_contains "│ ship it"
# the columns and cells are the grid now, not rows of their own
assert_not_contains "○ task"
assert_not_contains "○ ship it"

# --- the nodes face --------------------------------------------------------

send M-Down
wait_for "○ task"
assert_not_contains "┌"
assert_contains "○ ship it"   # nothing moved: the same outline that was typed

# --- back to the grid ------------------------------------------------------

send M-Up
wait_for "┌"
assert_contains "│ task"

# --- the grid editor -------------------------------------------------------

send M-e
wait_for "table · 1 × 1"
assert_contains "tab cell"

assert_no_crash
pass
