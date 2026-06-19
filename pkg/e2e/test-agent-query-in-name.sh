#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Behavior: an agent's QUERY is its name; children are context only. alt+shift+s
# ("ask a new agent this") makes the focused node's text the new agent's NAME —
# it must NOT become a child. No pi needed (no run).
#
# Repro:
#   1. Type "summarize the notes" in the notebook.
#   2. alt+shift+s — ask a new agent this.
#
# Expected:
#   - A worker named with the query appears: "✦ summarize the notes".
#   - It is NOT added as a context child: no "◆ summarize the notes".
#   - Nothing ran: no cost chip "$".

setup; launch

type "summarize the notes"
wait_for "○ summarize the notes"

send M-S                              # alt+shift+s
wait_for "✦ summarize the notes"     # the query is the agent's name

assert_contains "✦ summarize the notes"      # query lives in the name
assert_not_contains "◆ summarize the notes"  # NOT a context child
assert_not_contains "running"
assert_not_contains "\$0."                    # never ran

assert_no_crash
pass
