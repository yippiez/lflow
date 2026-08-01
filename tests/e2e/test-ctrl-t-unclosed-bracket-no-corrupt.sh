#!/usr/bin/env bash
# Regression test: ctrl+t on a row whose text begins with an unclosed bracket
# followed by a canonical ISO date must not corrupt the rendered pill.
#
# Bug: pressing ctrl+t when the text was "[2026-06-20" (stray leading bracket
# then a date) used to corrupt the rendered date pill — the bracket was not
# recognised as "inside a pill" context so a second conversion was attempted,
# nesting the result in extra bracket artifacts.
#
# Fix: commit 0009e03 — an unclosed [[ is now treated as an inside-pill span,
# suppressing a duplicate ctrl+t conversion.
#
# What this test verifies (current fixed binary):
#   1. After typing "[2026-06-20" and pressing ctrl+t the node text remains
#      exactly "[2026-06-20" — no extra bracket, no doubled content.
#   2. The status bar does NOT offer a ctrl+t hint (the date is already
#      canonical so no conversion should be proposed regardless of the bracket).
#   3. No "[[" artifact appears anywhere in the pane.
#   4. No crash / panic / runtime error.

set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

setup; launch

# Build scenario: create a single node whose text is "[2026-06-20" —
# a stray opening bracket immediately followed by a canonical ISO date.
type "[2026-06-20"
wait_for "○ [2026-06-20"

# Sanity: the text is there before we press ctrl+t.
assert_contains "[2026-06-20"

# The status bar must NOT be offering a ctrl+t conversion hint.
# (The date inside is already in canonical YYYY-MM-DD form, so even if the
# bracket were ignored the hint should not appear; and the bracket should
# suppress any match near the unclosed span.)
assert_not_contains "ctrl+t"

# Press ctrl+t — must be a no-op: no conversion, no corruption.
send "C-t"
wait_for "○ [2026-06-20"

# The row must still show exactly the original text with no extra brackets.
assert_contains "○ [2026-06-20"

# No "[[" artifact must appear anywhere in the pane — the old bug would nest
# a second pill attempt, producing "[[2026-06-20]]" or similar garbage.
assert_not_contains "[["

# Press ctrl+t a second time to be sure repeated invocations also do nothing.
send "C-t"
wait_for "○ [2026-06-20"
assert_contains "○ [2026-06-20"
assert_not_contains "[["

# The bug was a RENDERING corruption of the date pill, so the plain-text checks
# above are not enough on their own: a regression that broke the chip styling
# (date painted as bare text) or that mis-placed the chip span because of the
# stray leading bracket would still leave the plain capture reading
# "[2026-06-20". Verify on the STYLED capture that the date is still painted as
# a clean background chip (bgPill = #264f78 -> 48;2;38;79;120) sitting right
# after the bracket, and that the row carries no doubled/garbled escape runs.
chip_row="$(tmux capture-pane -t "${SESSION}" -p -e | grep '2026-06-20' || true)"
if [[ -z "${chip_row}" ]]; then
    LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
    fail "date row not found in styled capture after ctrl+t"
fi
# The date must still be a background-colored chip — not corrupted into plain text.
if [[ "${chip_row}" != *'48;2;38;79;120'* ]]; then
    LAST_PANE="${chip_row}"
    fail "date is not rendered as a background-colored chip (no bgPill escape)"
fi
# Exactly one chip background opener for this row — a nested/doubled pill (the
# old corruption) would paint the bgPill escape more than once.
chip_count="$(printf '%s' "${chip_row}" | grep -o '48;2;38;79;120' | wc -l | tr -d ' ')"
if [[ "${chip_count}" != "1" ]]; then
    LAST_PANE="${chip_row}"
    fail "expected exactly one date chip on the row, found ${chip_count} (pill nesting/corruption)"
fi
# The chipped span must contain exactly the bare date and nothing folded in:
# strip every CSI escape from the styled row, then confirm the colored chip's
# payload is precisely "2026-06-20" with the bracket left OUTSIDE it. The old
# corruption folded a bracket into the colored span ("[2026-06-20" or worse).
# We isolate the text between the bgPill opener and the next escape.
chip_payload="$(printf '%s' "${chip_row}" \
    | sed -E $'s/.*48;2;38;79;120m//; s/\x1b\\[.*//')"
if [[ "${chip_payload}" != "2026-06-20" ]]; then
    LAST_PANE="${chip_row}"
    fail "date chip payload is '${chip_payload}', expected bare '2026-06-20' (bracket folded into pill)"
fi

assert_no_crash
pass
