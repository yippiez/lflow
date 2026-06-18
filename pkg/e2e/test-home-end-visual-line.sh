#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: on a soft-wrapped long node, Home must move the caret to the
# START of the current visual line, not the start of the whole node text.
# Similarly, End on an intermediate visual line must stop at the end of that
# visual line, not at the end of the whole node text.
#
# Repro (Home):
#   WIN_W=40; type a sentence that wraps onto 2 visual lines; create a second
#   node below it; from the second node press Up to cross into visual line 2 of
#   the first node; press End (anchor away from line-start); press Home;
#   type "X".
#   BUG: X inserted at rune 0  → "Xthe quick..." on visual line 1.
#   FIX: X inserted at start of visual line 2 → "the quick..." unchanged on
#        line 1; "Xlazy..." visible on line 2.
#
# Repro (End):
#   A 3-visual-line node + a stopper node; from stopper Up crosses into the
#   last visual line; Up again to the intermediate visual line; End; type "Y".
#   BUG: Y appears after "today" (End jumped to the whole-node end).
#   FIX: "today" line follows BELOW Y in the pane (End stopped mid-node).
#
# Fix commit: 5159de1

# Width 40: "○ " prefix = 3 cols, leaving ~36 rune-width chars per visual line.
WIN_W=40

setup; launch

# -----------------------------------------------------------------------
# Part 1 – Home on a 2-line wrapped node  [core regression]
# -----------------------------------------------------------------------
# Sentence layout at width 40 (firstCol=3, avail=36 per line):
#   visual line 1 (runes 0-34):   "the quick brown fox jumps over the "
#   visual line 2 (runes 35-end): "lazy dog and then keeps running far"
type "the quick brown fox jumps over the lazy dog and then keeps running far"

wait_for "the quick brown fox"

# A sibling node below allows Up to cross the boundary cleanly into visual
# line 2 of the long node without risk of entering the Temp Domain.
send Enter
type "anchor"
wait_for "○ anchor"

# From "anchor", Up crosses the node boundary and lands on the LAST visual
# line of the long node (visual line 2, rune offset 35+).
send Up

assert_contains "the quick brown fox"

# Move caret to the END of visual line 2 (caret is non-trivially positioned)
# so that Home has to make a real move.
send End

# Home: FIXED → caret = start of visual line 2 (rune 35).
#       BROKEN → caret = 0 (absolute start of the whole node text).
send Home

# Type "X" to reveal where the caret landed.
type "X"

# Fixed: X is inserted at rune 35 → visual line 2 reads "Xlazy...".
# Broken: X is inserted at rune 0  → visual line 1 reads "Xthe quick...".
wait_for "Xlazy"

assert_contains "Xlazy"
# Regression guard: if Home jumped to rune 0, "Xthe" would appear.
assert_not_contains "Xthe"
# Visual line 1 must remain untouched.
assert_contains "the quick brown fox"

# -----------------------------------------------------------------------
# Part 2 – End on an intermediate visual line (not the last)
# -----------------------------------------------------------------------
# Navigate down to "anchor" (Down from the last visual line crosses to it).
send Down
wait_for "root · 2/2"

# Append text to "anchor" so it wraps across 3 visual lines:
#   line 1: "anchor plus the quick brown fox ru"  (runes 0-33)
#   line 2: "ns over the lazy bright blue dog to" (runes 34-68)
#   line 3: "day and keeps going far"             (runes 69-end)
# (Exact wrap boundaries depend on the renderer; we just need >= 3 lines.)
send End
type " plus the quick brown fox runs over the lazy bright blue dog today and keeps going far"
wait_for "going far"

# Add a stopper node below so Up can cross into "anchor"'s last visual line.
send Enter
type "stopper"
wait_for "○ stopper"

# From "stopper", Up crosses into the LAST visual line of the "anchor" node.
send Up

# Up again moves from the last visual line to the intermediate visual line.
send Up

# End on the intermediate visual line must stop at the end of THAT line,
# not at the end of the whole node text.
send End

# Type "Y" to reveal the insertion point.
type "Y"

# With the fix:  Y appears in the MIDDLE of the node; "going far" (which was
#               on the last visual line) must still appear BELOW Y in the pane.
# With the bug:  End jumped to the absolute node end, so Y appears after
#               "going far" — "going far" would be ABOVE Y in the pane.
pane="$(snapshot)"

if [[ "${pane}" != *"going far"* ]]; then
    fail "expected 'going far' to be present in the pane after End+Y insertion"
fi

y_line="$(printf '%s\n' "${pane}"       | grep -n 'Y'         | head -1 | cut -d: -f1)"
far_line="$(printf '%s\n' "${pane}"     | grep -n 'going far'  | head -1 | cut -d: -f1)"

if [[ -z "${y_line}" || -z "${far_line}" ]]; then
    fail "could not locate Y (line=${y_line}) or 'going far' (line=${far_line}) in the pane"
fi

# Y must appear ABOVE "going far" — End stopped on the intermediate visual
# line, so the last visual line's content follows below in the pane.
if ! (( y_line < far_line )); then
    fail "End on intermediate visual line moved caret to the node end (bug): Y on pane line ${y_line}, 'going far' on pane line ${far_line} — 'going far' must follow Y"
fi

assert_no_crash
pass
