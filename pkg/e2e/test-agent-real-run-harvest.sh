#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# End-to-end agent loop against a REAL pi model (gated by require_pi — SKIP when
# pi or ~/.pi is absent). Drives the whole workflow:
#   stage (alt+s) → run (alt+r) → reach a terminal state (no stuck idle) →
#   open the agent UI (alt+e) showing a "Final" outline → harvest (enter).
#
# Real models are non-deterministic, so we assert STRUCTURE (status/usage reached,
# Final section, harvest succeeded), never exact wording.

WIN_H=32
setup
require_pi          # SKIP unless a real pi + credentials are available
launch

type "what is the capital of France"
wait_for "what is the capital of France"

send M-s            # stage onto the agent (no run)
sleep 0.4
send M-r            # run it

# it must reach a terminal state, not hang — the stuck-idle bug regression guard.
# A cost chip ("$") only appears once usage has streamed back.
wait_for "\$" 45
# the worker settles to idle (process stays alive for steering) or done.
if ! { wait_for "idle" 45 || true; [[ "${LAST_PANE}" == *"idle"* || "${LAST_PANE}" == *"done"* ]]; }; then
    fail "agent did not reach a terminal state (stuck?)"
fi
assert_not_contains "error"     # no unhandled pi error
assert_no_crash

# open the agent UI and confirm the Final deliverable section renders
send Down
sleep 0.2
send M-e
wait_for "Final" 5
assert_contains "Agent"
assert_contains "Tool calls"
assert_no_crash

# harvest the deliverable into the notebook
send Escape
sleep 0.3
send Enter
wait_for "harvested" 5

assert_no_crash
pass
