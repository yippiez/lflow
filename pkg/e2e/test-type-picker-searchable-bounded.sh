#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: /type picker must be searchable and bounded to fit the window.
#
# Bug (pre-02fb5e1): with 11 node types the picker listed all of them
# unconditionally, overrunning short windows and pushing the status bar off-screen.
#
# Fix (02fb5e1):
#   - Typing in the picker filters by label (case-insensitive substring).
#   - A "type: <query>" search header is always shown at the top of the picker.
#   - The visible option list is capped at typePickerMaxRows (8) and scrolls.
#   - The picker reserves its own rows from the body budget so the status bar
#     always stays on-screen.
#   - Parenthetical suffixes were removed: "Worker (Pi agent)" -> "Worker",
#     "Query (codebase)" -> "Query", "Voice note" -> "Voice".
#
# Repro steps (from the bug report):
#   1. Use WIN_H=12 (a short window).
#   2. Type a node name and press Enter so the editor has at least one node.
#   3. Open the /type picker via "/type".
#   4. Type "qu" to filter — only "Query" should match.
#
# Expected (correct, post-fix) behavior:
#   - The "type: qu" search header is visible in the pane.
#   - Only "Query" (the matching label) appears as an option — not all 11 types.
#   - The status bar is still visible (not pushed off-screen).
#   - The label reads "Query" with NO parenthetical suffix.
#   - "Worker (Pi agent)" must never appear (clean label regression).

# Use a short 12-row window to reproduce the overflow condition.
WIN_H=12

setup; launch

# --- step 1: type a node so the outline is non-empty ---
type "node"
send Enter

wait_for "○ node"

# --- step 2: open the /type picker via the slash-command menu ---
# "/" opens the slash command menu.
type "/"
# Filter to the /type command.
type "type"
send Enter

# The /type picker is now open. The search header "type:" must appear.
wait_for "type:"

# --- step 2b: with NO filter, the picker lists all 12 types. In a 12-row
# window that would overrun and push the status bar off-screen unless the
# option list is capped (typePickerMaxRows ~8) and rows are reserved for the
# bar. Assert the cap holds AND the bar is still visible BEFORE filtering. ---
unfiltered="$(snapshot)"
# The status bar must still be on-screen even with every type listed.
assert_contains "2/2"
if (( $(printf '%s\n' "${unfiltered}" | grep -n '2/2' | head -1 | cut -d: -f1) > WIN_H )); then
    fail "unfiltered picker pushed the status bar off-screen (overran WIN_H=${WIN_H})"
fi
# The cap means later registry entries can't all fit: "Worker" (last type)
# must be scrolled out of view, proving the list is bounded, not dumped whole.
assert_not_contains "Worker"
# The early types are visible (the cap shows the top of the list).
assert_contains "Bullet"

# --- step 3: type "qu" to filter the picker ---
type "qu"

# Wait for the filtered header to reflect "qu".
wait_for "type: qu"

# --- assertions: correct post-fix behavior ---

# The filter header must show the query text.
assert_contains "type: qu"

# "Query" must appear as one of the matching options (matches "qu").
assert_contains "Query"

# "Quote" also matches "qu" — both must appear, but nothing else (like "Bullet").
assert_contains "Quote"

# The status bar must still be visible in the 12-row window.
# After pressing Enter a second (blank) node exists, so the bar shows "2/2".
assert_contains "2/2"

# The picker must NOT show all types — specifically, non-matching types like
# "Bullet" or "Heading 1" must not appear (they don't match "qu").
assert_not_contains "Bullet"
assert_not_contains "Heading 1"

# The old parenthetical labels must not appear (label-clean regression check).
assert_not_contains "Worker (Pi agent)"
assert_not_contains "Query (codebase)"
assert_not_contains "Voice note"

# Verify the status bar is within the 12-row window by checking row positions.
# The status bar must appear in the pane (not pushed off-screen).
pane="$(snapshot)"
bar_line="$(printf '%s\n' "${pane}" | grep -n '2/2' | head -1 | cut -d: -f1 || true)"
if [[ -z "${bar_line}" ]]; then
    fail "status bar (2/2) not found in pane — it was pushed off-screen by the picker"
fi
if (( bar_line > WIN_H )); then
    fail "status bar is on row ${bar_line}, outside WIN_H=${WIN_H} — picker overran the window"
fi

# The picker header must also be within the window bounds.
header_line="$(printf '%s\n' "${pane}" | grep -n 'type: qu' | head -1 | cut -d: -f1 || true)"
if [[ -z "${header_line}" ]]; then
    fail "type picker search header 'type: qu' not found in pane"
fi
if (( header_line > WIN_H )); then
    fail "type picker header is on row ${header_line}, outside WIN_H=${WIN_H}"
fi

assert_no_crash
pass
