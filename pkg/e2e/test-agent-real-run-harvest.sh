#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# End-to-end agent loop against a REAL pi model (gated by require_pi — SKIP when
# pi or ~/.pi is absent). Drives the whole workflow:
#   alt+shift+s on a note → launches+runs an agent in the Agent Domain (the note
#   is consumed) → reach a terminal state (no stuck idle) → open the agent view
#   (alt+e) showing a "Final" outline → harvest (enter).
#
# Real models are non-deterministic, so we assert STRUCTURE (status/usage reached,
# Final section, harvest succeeded), never exact wording.

WIN_H=32
setup
require_pi          # SKIP unless a real pi + credentials are available
launch

type "what is the capital of France"
wait_for "what is the capital of France"

send M-S            # launch an agent on this note (query=note) and RUN it now

# it must reach a terminal state, not hang — the stuck-idle bug regression guard.
# A cost chip ("$") only appears once usage has streamed back. Generous timeouts:
# this is a live model call and the suite runs it under load.
wait_for "\$" 90
# the worker settles to idle (process stays alive for steering) or done.
if ! { wait_for "idle" 90 || true; [[ "${LAST_PANE}" == *"idle"* || "${LAST_PANE}" == *"done"* ]]; }; then
    fail "agent did not reach a terminal state (stuck?)"
fi
assert_not_contains "error"     # no unhandled pi error
assert_no_crash

# open the agent view (inline) and confirm the Final deliverable section renders.
# In the Agent Domain the first node is the empty compose line, so step past it to
# the launched agent (the second node), then alt+e.
send Down            # into the Agent Domain (compose line)
sleep 0.2
send Down            # onto the launched agent
sleep 0.2
send M-e
wait_for "Final" 8
assert_contains "Agent"
assert_contains "Tool calls"
assert_no_crash

# harvest the deliverable into the notebook (flash "harvested N" may be clipped in
# the status bar, so match the prefix)
send Escape
sleep 0.3
send Enter
wait_for "harvest" 8

assert_no_crash
pass
