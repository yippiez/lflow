#!/usr/bin/env bash
# Regression test: CRLF / control-char paste must not create empty ghost nodes.
#
# Bug (fixed in 01e0ced): pasting text with CRLF line endings plus a blank line
# (e.g. "one\r\n\r\ntwo\r\n") caused the old naive replacement to produce an
# empty string between the two real lines — that empty string became a ghost node
# with no name, corrupting the outline.
#
# Fix: pasteLines() now uses a [\r\n]+ regex that collapses any run of CR/LF
# into a single break, so the blank CRLF-only line vanishes. pasteFanOut() also
# skips any line that sanitized to empty, providing a second safety net.
#
# What this test asserts (correct post-fix behavior):
#   1. After pasting "one\r\n\r\ntwo\r\n" exactly two nodes appear: "one" and "two".
#   2. No empty/blank node bullet appears between them (no ghost node).
#   3. The pane shows no stray control characters (no broken escapes).
#   4. No crash / panic / runtime error.
#
# The bracketed-paste sequence is injected via raw escape bytes so bubbletea
# sees k.Paste == true and routes through pasteLines(), exactly replicating what
# a real terminal paste does.

set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

setup; launch

# Create a first node "alpha" so the cursor is on a named node (not the blank
# initial row). Confirm it with Enter, which creates a new sibling below.
type "alpha"
wait_for "○ alpha"
send Enter

# Sanity: we are on a fresh empty row below "alpha".
wait_for "○ alpha"

# Send a bracketed-paste containing "one\r\n\r\ntwo\r\n":
#   ESC[200~  = bracketed-paste start
#   one\r\n   = first line with CRLF
#   \r\n      = blank CRLF-only line (the old bug turned this into a ghost node)
#   two\r\n   = third line with CRLF
#   ESC[201~  = bracketed-paste end
#
# tmux send-keys -l sends the bytes literally; the leading \x1b is the ESC byte.
# We use printf to produce the exact byte sequence and pipe it through
# tmux load-buffer + paste-buffer so the full sequence is delivered atomically.
PASTE_SEQ="$(printf '\x1b[200~one\r\n\r\ntwo\r\n\x1b[201~')"
tmux load-buffer - <<< "${PASTE_SEQ}"
tmux paste-buffer -t "${SESSION}"
sleep 0.3

# Wait for "two" to appear — it is the last pasted line so if it's visible the
# paste has been processed.
wait_for "○ two"

# Assert both real nodes are present.
assert_contains "○ one"
assert_contains "○ two"

# The original alpha node must still be there.
assert_contains "○ alpha"

# Verify ordering: alpha then one then two (top-to-bottom in pane).
pane="$(snapshot)"
alpha_line="$(printf '%s\n' "${pane}" | grep -n '○ alpha' | head -1 | cut -d: -f1)"
one_line="$(printf '%s\n' "${pane}" | grep -n '○ one' | head -1 | cut -d: -f1)"
two_line="$(printf '%s\n' "${pane}" | grep -n '○ two' | head -1 | cut -d: -f1)"

if ! (( alpha_line < one_line && one_line < two_line )); then
    fail "expected order alpha < one < two, got lines alpha=${alpha_line} one=${one_line} two=${two_line}"
fi

# Count bullet lines between "one" and "two". A ghost node renders as a bare
# bullet with NO name after it ("○" at end of line, no trailing text), so the
# match must be the bullet glyph itself — matching "○ " (bullet+space) would
# miss the ghost rows entirely since the empty node prints no space. Exactly
# zero bullet rows should appear strictly between one_line and two_line.
ghost_count="$(printf '%s\n' "${pane}" | awk -v lo="${one_line}" -v hi="${two_line}" \
    'NR > lo && NR < hi && /○/ {count++} END {print count+0}')"
if (( ghost_count > 0 )); then
    fail "found ${ghost_count} ghost node(s) between 'one' and 'two' — CRLF blank line was not collapsed"
fi

# Belt-and-suspenders: the node count in the status bar must be exactly 4
# (root + alpha + one + two). A ghost node inflates this count even if pane
# layout shifts the ghost row off-screen.
if ! printf '%s\n' "${pane}" | grep -qE '4/4'; then
    fail "expected node count 4/4 (root+alpha+one+two), status bar shows otherwise — possible ghost node"
fi

# No stray control characters: the pane text (captured plain) must not contain
# ESC bytes (would indicate broken escape sequences leaked into render).
if printf '%s\n' "${pane}" | grep -qP '\x1b'; then
    fail "pane contains stray ESC bytes — control chars leaked into rendering"
fi

assert_no_crash
pass
