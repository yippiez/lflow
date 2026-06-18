#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Repro: type a node name containing a date phrase ("meet 2026-06-20"), then
# press ctrl+t to convert it. The bug was that bracket markup [[ ]] was rendered
# around the date chip. The fix stores no brackets and renders a plain colored
# chip — so the plain-text pane must contain "2026-06-20" with no surrounding
# [[ or ]] characters.

setup; launch

# Type a node whose name contains an ISO date phrase.
type "meet 2026-06-20"

# Wait for the text to appear in the pane.
wait_for "meet 2026-06-20"

# Press ctrl+t to convert the date phrase at the caret.
send C-t

# After conversion the node name is "meet 2026-06-20" (canonically unchanged
# since it was already ISO). The date is shown as a chip with background colour
# but the plain-text capture must show the bare date without any bracket wrapper.
wait_for "2026-06-20"

# Core assertion: the date chip text must appear — the node was not erased.
assert_contains "2026-06-20"

# Bug regression: [[ ]] brackets must NOT appear around the date chip.
assert_not_contains "[["
assert_not_contains "]]"
# Also reject single-bracket wrappers around the date.
assert_not_contains "[2026-06-20]"

# The other half of the fix: the date is a BACKGROUND-COLORED chip, not plain
# text. The plain capture above strips SGR, so verify the chip's background
# escape (bgPill = #264f78 -> 48;2;38;79;120) is actually painted on the row
# carrying the date. Without this, a regression that dropped the chip styling
# (rendering the date as bare plain text) would still pass the bracket checks.
chip_row="$(tmux capture-pane -t "${SESSION}" -p -e | grep '2026-06-20' || true)"
if [[ -z "${chip_row}" ]]; then
    LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
    fail "date row not found in escape capture"
fi
if [[ "${chip_row}" != *'48;2;38;79;120'* ]]; then
    LAST_PANE="${chip_row}"
    fail "date is not rendered as a background-colored chip (no bgPill escape)"
fi
# And the brackets must not appear in the styled capture either.
if [[ "${chip_row}" == *'[['* || "${chip_row}" == *']]'* ]]; then
    LAST_PANE="${chip_row}"
    fail "bracket markup present around date chip in styled capture"
fi

assert_no_crash
pass
