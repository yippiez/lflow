#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# alt+y copies a branch AS DRAWN — rail, connectors and bullets (see
# nodesAsRail) — so pasting it back has to read the depth off that rail and
# leave the glyphs behind. The bug this guards: the rail landing in the node
# names, "○ beta" instead of "beta", every row at the same depth.
#
# The paste is injected as a bracketed-paste escape so bubbletea sees a real
# paste, exactly like test-crlf-paste-no-ghost-nodes.sh.

setup; launch

type "root node"
wait_for "○ root node"
send Enter

# printf straight into load-buffer: a here-string would append a newline, and
# that trailing Enter would open an empty node under the last pasted row.
printf '\x1b[200~ \xe2\x97\x8b alpha\r\n \xe2\x94\x9c\xe2\x94\x80 \xe2\x97\x8b beta\r\n \xe2\x94\x82  \xe2\x95\xb0\xe2\x94\x80 \xe2\x97\x8b gamma\r\n \xe2\x95\xb0\xe2\x94\x80 \xe2\x97\x8b delta\x1b[201~' | tmux load-buffer -
tmux paste-buffer -t "${SESSION}"
sleep 0.3

wait_for "○ gamma"

# the rail rebuilt the tree: beta and delta under alpha, gamma under beta
assert_contains "○ alpha"
assert_contains "├─ ○ beta"
assert_contains "│  ╰─ ○ gamma"
assert_contains "╰─ ○ delta"

# and none of it landed in the names
assert_not_contains "○ ○"
assert_not_contains "○ ├─"

assert_no_crash
pass
