#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: a mirror's note band must show the live in-memory note of its
# source immediately — without requiring a save — so unsaved edits on the source
# are visible through the mirror at once.
#
# Before fix 20e4b09: displayNote on a mirror returned the mirror's own stale
# note field (empty / last-saved value) rather than the source's live note.
# The note band under the mirror was therefore either absent or showed stale text.
#
# Repro (condensed from the original report):
#   1. Type "source" as the first node name.
#   2. Add a note "init note" to the source via /note, confirm with Enter.
#   3. Save (C-s) so the finder can locate the node.
#   4. Open a blank sibling (Enter), then mirror it to "source" via /mirror.
#   5. Navigate back to source; enter /note mode, append " LIVE", confirm (Enter).
#      The note is now "init note LIVE" — unsaved — on the source.
#   6. Navigate to the mirror row.
#   7. Assert the mirror's note band (the line appearing BELOW "◆ source") shows
#      "init note LIVE" without a save.  In the buggy version that line is absent
#      (mirror.note == "" stale), so the assertion catches the regression.

setup; launch

# ── step 1: create the source node ──────────────────────────────────────────
type "source"
wait_for "○ source"

# ── step 2: add an initial note via /note ───────────────────────────────────
# type "/" to open slash menu, then "note" to filter, then Enter to run /note
send "/"
type "note"
wait_for "/note"
send Enter
# now in note mode; type the initial note text
type "init note"
# confirm note (Enter exits note mode; the note is stored in memory)
send Enter
wait_for "○ source"

# ── step 3: save so the finder can locate the source node ───────────────────
send C-s

# ── step 4: create a blank sibling and mirror it to "source" ────────────────
# Enter creates a sibling below; the blank node becomes our mirror target
send Enter
# blank sibling is now selected; open /mirror via slash menu
send "/"
type "mirror"
wait_for "/mirror"
send Enter
# the fuzzy finder opens in /mirror mode — wait for its label to appear
wait_for "/mirror" 5
# "source" is the only saved node; select it with Enter
send Enter
# wait for the mirror glyph to appear
wait_for "◆ source" 5
assert_contains "◆ source"

# ── step 5: navigate to source and append to its note (unsaved) ─────────────
send Up
wait_for "○ source"
# enter /note mode on the source
send "/"
type "note"
wait_for "/note"
send Enter
# caret starts at end of "init note" (length 9); append " LIVE"
type " LIVE"
# confirm the note; the source's in-memory note is now "init note LIVE", unsaved
send Enter
wait_for "○ source"

# ── step 6: navigate to the mirror ──────────────────────────────────────────
send Down
wait_for "◆ source" 5

# ── step 7: assert the mirror's note band shows the live note ───────────────
# Capture the pane and find the line number of the mirror row.
# The note band ("init note LIVE") must appear on a line AFTER the mirror row.
# In the buggy version, the mirror's note band was absent (stale mirror.note="")
# so nothing appeared below the "◆ source" line, and the check below would fail.
pane="$(snapshot)"
mirror_line="$(printf '%s\n' "${pane}" | grep -n '◆ source' | head -1 | cut -d: -f1)"
if [[ -z "${mirror_line}" ]]; then
    fail "could not find mirror row '◆ source' in pane"
fi
note_below="$(printf '%s\n' "${pane}" | awk -v m="${mirror_line}" 'NR > m' | grep -F "init note LIVE" || true)"
if [[ -z "${note_below}" ]]; then
    fail "mirror note band should show 'init note LIVE' below the mirror row (line ${mirror_line}); got no such line after it"
fi

assert_no_crash
pass
