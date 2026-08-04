#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# /note is a real rich-text surface: tag gestures and /insert kinds land in the
# note, then return to note editing rather than mutating the node title.

setup; launch

type "task title"
type "/"
type "note"
send Enter

type "#"
type "qol"
send Space
send M-a
type "date"
send Enter
send Enter

wait_for "#qol"
today="$(date +%F)"
assert_contains "${today}"
assert_contains "○ task title"
assert_no_crash
pass
