#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# lflow file open <x.py>: the file parses into python nodes (body nested under
# its header, keywords rendered inline), and ctrl+s serializes the edited tree
# back to the file with proper 4-space indentation — two-way binding.
#
# Steps:
#   1. write a small app.py; launch `file open app.py`
#   2. the outline shows the def header with its body as a child
#   3. append a new body line, ctrl+s
#   4. the file on disk gains the line, indented under the def

setup

PYFILE="${TEST_HOME}/app.py"
cat > "${PYFILE}" <<'EOF'
def greet():
    return 1
EOF

launch file open "${PYFILE}"

# the file parsed into an outline: the def is a first-class fn node (ƒ glyph,
# keyword-free signature) with its body as a child
wait_for "ƒ greet()"
wait_for "return 1"

# the header shows the file name, not Root
assert_contains "app.py"

# grow the body: enter a sibling below `return 1` — cursor starts on the
# header row, walk down to the body line first
send Down
send End
send Enter
type "x = 2"
send C-s

# the save serialized the tree back through the codec
wait_for "app.py"
sleep 0.5

content="$(cat "${PYFILE}")"
case "${content}" in
    *"def greet():"*) : ;;
    *) fail "def header missing from written file: ${content}" ;;
esac
case "${content}" in
    *"    x = 2"*) : ;;
    *) fail "new body line not written 4-space indented: ${content}" ;;
esac

assert_no_crash
pass
