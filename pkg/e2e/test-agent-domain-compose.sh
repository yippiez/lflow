#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Behavior: the bottom space is the "Agent Domain". Its first node is always an
# empty compose line (a ◌ ✦ worker) you can type into; Enter would launch it. This
# test covers the rename + the compose line WITHOUT launching (no pi needed —
# launching runs a real agent, which test-agent-real-run-harvest covers).
#
# Repro:
#   1. Launch; type a note so the main outline isn't empty.
#   2. Down — focus crosses into the Agent Domain (status shows "Agent Domain").
#   3. The first node is an empty ◌ ✦ compose line; type into it.

setup; launch

type "a note"
wait_for "○ a note"

send Down
wait_for "Agent Domain"            # the bottom space is relabeled
assert_contains "◌"                # dashed agent glyph (compose line)

type "ask something"
wait_for "ask something"           # the compose line accepts text
assert_contains "ask something"
assert_contains "Agent Domain"
assert_not_contains "Temporary Domain"

assert_no_crash
pass
