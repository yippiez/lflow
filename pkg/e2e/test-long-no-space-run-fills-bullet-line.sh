#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: a long unbroken run wider than the first row must fill the bullet
# line, not leave it blank with all text starting one visual row below.
#
# With WIN_W=50 and hanging indent of 3 cols ("  " before and after "○"), the
# first row has 47 columns available for the body after the bullet.  A 120-char
# no-space run must therefore start on the bullet row itself — the node must
# render as "○ xxxxx..." (47 x's) on row 1, then continuation rows of "   xxx..."
# with the hanging indent.
#
# The pre-fix behaviour was that `wrapLine` chose the first space (the one after
# the "○" glyph) as a wrap point, leaving the bullet row blank and pushing all
# text down one visual row.  That caused Home to place the caret on row 2 instead
# of row 1.
#
# Expected post-fix behaviour:
#   - The bullet row contains "○ " immediately followed by x characters ("○ xx").
#   - No line in the pane consists solely of the bullet glyph "○" with nothing
#     after it (which was the hallmark of the regression).
#
# Fix commit: 2f95574

WIN_W=50

setup; launch

# Type a 120-character no-space run using a single repeated character.
# 120 chars >> 50-col window, so wrapping is forced.
LONG_RUN="$(printf '%0.sx' {1..120})"
type "${LONG_RUN}"

# Wait until the editor has rendered enough x's to confirm the text is on screen.
wait_for "○ xxx"

# CORE ASSERTION: the bullet line must be filled — "○ " must appear on the
# same visual row as x characters.  A substring of "○ xx" is present iff the
# bullet and at least two body characters share the same terminal row.
assert_contains "○ xx"

# REGRESSION GUARD: the old bug left the bullet row with just the glyph and
# nothing after it, so a terminal row containing ONLY "○" (no x following it
# on that row) would appear.  Post-fix that must never happen.
# We detect it by checking that no pane line matches "○$" (glyph at end of
# line with nothing after it).
pane="$(snapshot)"
bad_line="$(printf '%s\n' "${pane}" | grep -P '○\s*$' || true)"
if [[ -n "${bad_line}" ]]; then
    fail "bullet row is blank (the regression): found a line ending right after '○': ${bad_line}"
fi

assert_no_crash
pass
