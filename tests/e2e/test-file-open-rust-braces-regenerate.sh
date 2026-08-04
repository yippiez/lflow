#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# lflow file open <x.rs>: a rust file parses into fn/statement nodes (closing
# braces are structure, not text) and ctrl+s regenerates the braces from the
# tree — adding a body line writes back inside the block.

setup

RSFILE="${TEST_HOME}/lib.rs"
cat > "${RSFILE}" <<'EOF'
fn double(x: i32) -> i32 {
    x * 2
}
EOF

launch file open "${RSFILE}"

# the fn is a first-class node (keyword-free signature), body nested, no `}` row
wait_for "double(x: i32) -> i32"
wait_for "x * 2"

# add a statement inside the block: walk to the body line, open a sibling
# (no `;` in the typed text — tmux send-keys treats a trailing one as a
# command separator)
send Down
send End
send Enter
type "let y = x"
send C-s

sleep 0.5

content="$(cat "${RSFILE}")"
case "${content}" in
    *"fn double(x: i32) -> i32 {"*) : ;;
    *) fail "fn header not regenerated: ${content}" ;;
esac
case "${content}" in
    *"    let y = x"*) : ;;
    *) fail "new statement not written inside the block: ${content}" ;;
esac
# exactly one closing brace at column 0
braces="$(grep -c '^}' "${RSFILE}" || true)"
if [[ "${braces}" != "1" ]]; then
    fail "expected exactly one closing brace, got ${braces}: ${content}"
fi

assert_no_crash
pass
