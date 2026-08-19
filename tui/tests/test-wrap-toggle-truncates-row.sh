#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# /wrap:toggle turns a node's row from soft-wrapping into truncating: the text
# stops at the right edge with a … and the node stays ONE line however long it
# is. Toggling again brings the wrap back.
#
# Repro steps (all against a :memory: DB in tmux):
#   1. setup; launch
#   2. type a name far wider than the 80-column pane
#   3. it wraps: head and tail are both on screen, on two lines
#   4. /wrap:toggle → one line, scrolled to the caret: the tail shows, the head
#      is cut away behind a …
#   5. Home → the window comes back to the left: the head shows, the tail is cut
#   6. /wrap:toggle again → both are back

setup; launch

HEAD="ALPHAHEAD bravo charlie delta echo foxtrot golf hotel india juliett kilo"
TAIL="ZULUTAIL"

type "${HEAD} ${TAIL}"
wait_for "${TAIL}"
assert_contains "ALPHAHEAD"

toggle_wrap() {
    send /
    type "wrap"
    wait_for "/wrap:toggle"
    send Enter
}

# Step 4: one line, following the caret at the end of the text.
toggle_wrap
wait_for "line wrap off"
assert_contains "${TAIL}"
assert_contains "…"
assert_not_contains "ALPHAHEAD"

# Step 5: the window follows the caret back to the start of the line.
send Home
wait_for "ALPHAHEAD"
assert_not_contains "${TAIL}"

# Step 6: back to wrapping — the whole text is on screen again.
toggle_wrap
wait_for "line wrap on"
assert_contains "ALPHAHEAD"
assert_contains "${TAIL}"

assert_no_crash
pass
