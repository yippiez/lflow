#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Behavior: alt+s STAGES a notebook node onto an agent as context — it must NOT
# run the agent, and must NOT set the agent's title (only add a child node).
# Running is alt+r. No pi needed: staging is a pure local edit.
#
# Repro:
#   1. Type "delegate me" in the notebook.
#   2. alt+s — stage it onto the temp worker.
#
# Expected:
#   - A mirror child "◆ delegate me" appears under the worker.
#   - The worker's title is NOT set: "✦ delegate me" must NOT appear.
#   - Nothing ran: no cost chip "$", no "running" status.

setup; launch

type "delegate me"
wait_for "○ delegate me"

send M-s
wait_for "◆ delegate me"          # staged as a context mirror child

assert_contains "◆ delegate me"   # the child node was added
assert_not_contains "✦ delegate me"  # the worker title was NOT set
assert_not_contains "running"     # alt+s did not run the agent
assert_not_contains "\$0."        # no usage/cost chip → never ran

assert_no_crash
pass
