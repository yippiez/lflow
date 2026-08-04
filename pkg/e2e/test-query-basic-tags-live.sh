#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Basic #tag expressions are live-query atoms. They must match ordinary tag
# chips, including adjacent tag intersections, without reporting a completed or
# otherwise stale result set.

setup; launch

type "first "
type "#"
type "node"
send Space
type "#"
type "qol"
send Space
send Enter

type "second "
type "#"
type "node"
send Space
send Enter

type "/"
type "type"
send Enter
type "query"
send Enter
type "#node #qol"
send M-r
wait_for "1 hits" 8
assert_contains "first #node #qol"
assert_not_contains "0 hits"
assert_no_crash
pass
