#!/usr/bin/env bash
# test-bash-node-shows-dollar-and-spinner.sh
#
# Regression: a Bash node renders as "$ <cmd>" — the red "$" IS the row's glyph
# (the cmd chip's prompt, always, fold or no fold), so there is no plain bullet.
# A run streams LIVE into the row's "→" tail (the same surface a cmd chip's tail
# is — the newest line while in flight, the first once settled), never a band
# beneath the row and never a ✓ or ✗ completion glyph.
#
# The bug this guards: the "$ " glyph and the streaming tail were absent, and a
# ✓/✗ glyph could appear on completion instead of keeping the "$".
#
# Repro steps:
#   1. Type a command, confirm it starts as a plain bullet (no "$ " sign yet).
#   2. Open /type picker, filter to "Bash", select it.
#   3. Assert the node now renders as "$ <cmd>" (the red $ glyph replaces the
#      bullet).
#   4. Press M-r to run it.
#   5. Assert the COMPUTED output streams into the row's tail as "→ 13".
#   6. After completion the row still shows "$ ", the tail is settled, and there
#      is no ✓ or ✗ glyph anywhere in the pane.
#
# The command is `sleep 1; echo $((6 + 7))`:
#   - the output token "13" is NOT a substring of the command text, so waiting on
#     "13" genuinely proves the OUTPUT rendered (not the echoed command);
#   - `echo` emits a newline so the line flushes promptly and the run completes.

set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# The literal command we drive into the node. Kept in a single-quoted var so the
# $(( )) is never expanded by THIS shell.
CMD='sleep 1; echo $((6 + 7))'

setup
launch

# Step 1: type the command text. A freshly typed node is a plain bullet with NO
# "$ " sign — confirm that so the post-conversion "$ " is a real difference.
type "$CMD"
wait_for "○ $CMD"
assert_not_contains "○ \$ $CMD"

# Step 2: open the slash-command picker and filter to /type (bounded scrolling list).
send /
send t
send y
send p
send e
wait_for "/type"
send Enter

# Step 3: the /type picker is open; filter to "Bash" and select it.
wait_for "type to search"
send b
send a
send s
send h
wait_for "Bash"
send Enter

# The node is now a Bash node. It must render with the "$" as its glyph — the
# bullet is REPLACED, not kept, so the row reads "$ <cmd>".
wait_for "\$ $CMD" 5

# Step 4: press M-r (alt+r) to run the command.
send M-r

# Step 5: wait for the COMPUTED output (13) to stream into the row's tail as
# "→ 13". "13" does not appear in the command text, so this can only match the
# rendered run output — and its being in the TAIL (same row, no band) is what
# the merged design guarantees.
wait_for "→ 13" 8

# After completion the row must STILL show the "$ " glyph (the run surface is the
# tail, the glyph never replaced by a completion mark), the output stays in the
# tail, and there is no ✓ or ✗ anywhere.
assert_contains "\$ $CMD"
assert_contains "→ 13"
assert_not_contains "✓"
assert_not_contains "✗"

assert_no_crash
pass
