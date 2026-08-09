#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# lflow file open <x.py>: a statement added in the blank gap between two
# top-level lines stays at the top level. The blank line is a spacer node, and
# it used to hang off the line ABOVE it — so the gap after `import os` was a
# CHILD of that import, every row born beside it was born one level deeper, and
# ctrl+s wrote `    import sys` indented under `import os`: not python any more.
#
# Steps:
#   1. write a file whose top-level lines are separated by a blank line
#   2. the import shows no ` · n lines` tail — the gap is not its body
#   3. put the caret on the blank row, Enter, type a new import, ctrl+s
#   4. the file on disk has the new import at column 0, and still parses

setup

PYFILE="${TEST_HOME}/gap.py"
cat > "${PYFILE}" <<'EOF'
import os


def f():
    return 1
EOF

launch file open "${PYFILE}"

wait_for "import os"
wait_for "f()"

# the gap is not the import's body: no fold tail counting the blank lines
assert_not_contains "import os  · 2 lines"

# caret onto the first blank row (row 2 of the outline), open a row, type there
send Down
send Enter
type "import sys"
send C-s

sleep 0.5

content="$(cat "${PYFILE}")"
case "${content}" in
    *$'\nimport sys'*) : ;;
    *) fail "new import did not land at the top level: ${content}" ;;
esac
case "${content}" in
    *"    import sys"*) fail "new import was indented into the line above: ${content}" ;;
    *) : ;;
esac

# the written file is still valid python
if command -v python3 >/dev/null 2>&1; then
    if ! python3 -c "import ast,sys; ast.parse(open(sys.argv[1]).read())" "${PYFILE}"; then
        fail "saved file is not valid python: ${content}"
    fi
fi

assert_no_crash
pass
