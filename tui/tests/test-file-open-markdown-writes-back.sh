#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# lflow file open <x.md>: markdown maps onto the existing node types (heading,
# todo, bullets) and ctrl+s writes the edited outline back as markdown.
#
# Steps:
#   1. write notes.md with a heading and a todo; launch `file open notes.md`
#   2. the outline shows the heading with the todo nested under it
#   3. add a new item, ctrl+s
#   4. the file on disk gains "- [ ] new item" under the section (Down crosses
#      straight onto the todo, so the sibling inherits its todo type)

setup

MDFILE="${TEST_HOME}/notes.md"
cat > "${MDFILE}" <<'EOF'
# Project

- [ ] write the parser
EOF

launch file open "${MDFILE}"

# heading renders with its level glyph, the todo with its box
wait_for "Project"
wait_for "write the parser"

# add a sibling item after the todo
send Down
send End
send Enter
type "new item"
send C-s

sleep 0.5

content="$(cat "${MDFILE}")"
case "${content}" in
    *"# Project"*) : ;;
    *) fail "heading missing from written file: ${content}" ;;
esac
case "${content}" in
    *"- [ ] write the parser"*) : ;;
    *) fail "todo missing from written file: ${content}" ;;
esac
case "${content}" in
    *"- [ ] new item"*) : ;;
    *) fail "new item not written back: ${content}" ;;
esac

assert_no_crash
pass
