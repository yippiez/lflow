#!/usr/bin/env bash
# test-bash-node-shows-dollar-and-spinner.sh
#
# Regression: a Bash node must render as "○ $ <cmd>" — the plain bullet is KEPT
# and a gray "$ " sign is prefixed to the command. While the command runs a
# "running…" band shows beneath the row (the run indicator), and once it finishes
# the row reverts to a PLAIN bullet with the output streamed into a gray band —
# never a ✓ or ✗ completion glyph.
#
# The bug this guards: the "$ " sign prefix and the run-output band were absent,
# and a ✓/✗ glyph could appear on completion instead of keeping the plain bullet.
#
# Repro steps:
#   1. Type a command, confirm it starts as a plain bullet (no "$ " sign yet).
#   2. Open /type picker, filter to "Bash", select it.
#   3. Assert the node now renders as "○ $ <cmd>" (bullet kept + dollar sign).
#   4. Press M-r to run it.
#   5. While running, assert the "running…" band appears beneath the node.
#   6. After completion, assert the COMPUTED output appears in the band, the
#      "running…" indicator is gone, the row still shows "○ $ ", and there is no
#      ✓ or ✗ glyph anywhere in the pane.
#
# The command is `sleep 1; echo $((6 + 7))`:
#   - the leading `sleep 1` keeps the "running…" band on screen long enough to
#     observe it deterministically;
#   - the output token "13" is NOT a substring of the command text, so waiting on
#     "13" genuinely proves the OUTPUT BAND rendered (not the echoed command);
#   - `echo` emits a newline so the line flushes promptly and the run completes,
#     clearing the "running…" band before the assertions run.

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

# Step 2: open the slash-command picker and navigate to /type.
send /
wait_for "/type" 5
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

# The node is now a Bash node. It must render with the bullet KEPT and a "$ "
# sign prefixed to the command — this contiguous match proves the sign sits right
# after the bullet (catches both a missing sign and a replaced bullet).
wait_for "○ \$ $CMD" 5

# Step 4: press M-r (alt+r) to run the command.
send M-r

# Step 5: while running, a "running…" band must appear beneath the node — this is
# the run indicator. sleep 1 keeps it on screen long enough to observe.
wait_for "running" 5

# Step 6: wait for the COMPUTED output (13) to stream into the band. "13" does not
# appear in the command text, so this can only match the rendered output band.
wait_for "13" 8

# After completion the row must STILL show the plain bullet + "$ " sign (reverted
# from the running state, bullet never replaced by a completion glyph), the
# "running…" indicator must be gone, and there must be no ✓ or ✗ anywhere.
assert_contains "○ \$ $CMD"
assert_contains "13"
assert_not_contains "running"
assert_not_contains "✓"
assert_not_contains "✗"

assert_no_crash
pass
