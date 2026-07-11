#!/usr/bin/env bash
# test-mirror-outdent-escapes-source.sh
#
# Regression: shift+tab on a direct child shown THROUGH a mirror must move that
# child out of the source and land it as the next sibling of the mirror. Both
# the original and the mirror then stop showing the child.
#
# Before: outdent was bounded at the mirror's source (localRoot), so shift+tab
# on a through-child was a no-op at the mirror root.
#
# Scenario (before BTab):
#   ○ src
#   ╰─ ○ kid              <- real child of src
#   ○ src · mirror
#   ╰─ ○ kid              <- shown through; cursor here, then BTab
#
# After BTab:
#   ○ src                 (no children)
#   ○ src · mirror        (no through children)
#   ○ kid                 (real sibling after the mirror)
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

setup; launch

# --- Build src > kid -------------------------------------------------------
type "src"
send Enter
type "kid"
send Tab
wait_for "╰─ ○ kid"
assert_contains "○ src"

# --- Empty sibling of src, then /mirror:to src -----------------------------
# From kid: Enter opens a sibling under src; BTab outdents it next to src.
send Enter
send BTab
wait_for "○ src"

# Save so the finder's DB search can see src.
send C-s
wait_for "3/3"

# Slash menu: type the filter before waiting — /mirror:to sits below the first
# page of the unfiltered list on a 24-row pane.
send "/"
type "mirror:to"
wait_for "/mirror:to"
send Enter
# Finder open; narrow to src and select it.
wait_for "src"
type "src"
wait_for "src"
send Enter

wait_for "src · mirror"
assert_contains "○ src"
assert_contains "╰─ ○ kid"

# --- Cursor onto the through-row of kid under the mirror -------------------
# Rows: src, kid (orig), mirror header, kid (through) → 4/4.
# Cursor is on the mirror after /mirror:to; one Down lands on through-kid.
send Down
wait_for "4/4"

# --- Shift+Tab: escape the mirror root -------------------------------------
send BTab

# kid left the source: three top-level rows, none indented, no through copy.
wait_for "3/3"
assert_contains "○ src"
assert_contains "src · mirror"
assert_contains "○ kid"
assert_not_contains "╰─ ○ kid"
assert_not_contains "├─ ○ kid"

assert_no_crash
pass
