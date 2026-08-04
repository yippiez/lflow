#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# A bash node's result is persisted, but the ROW used to forget it: reopening the
# outline showed a bare command until alt+e went and fetched the band, which
# reads exactly like the result was thrown away. The row must hang its
# "→ <result>" tail again the moment the outline opens.

setup
use_persist_db
launch

type "echo persisted-output"
type "/"
type "type"
send Enter
type "Bash"
wait_for "Bash"
send Enter
wait_for "$ echo persisted-output" 8

send M-r
wait_for "→ persisted-output" 10
send C-s
wait_for "persisted-output"

reopen

# the tail is back WITHOUT opening the band
wait_for "→ persisted-output" 10
assert_contains "→ persisted-output"
assert_no_crash
pass
