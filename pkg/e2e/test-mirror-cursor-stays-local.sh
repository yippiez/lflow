#!/usr/bin/env bash
# test-mirror-cursor-stays-local.sh
#
# Regression for: "Cursor stays local to the mirror when editing through it"
# Fix commit: 6b83226
#
# The bug: pressing Enter on a node shown THROUGH a mirror created a new sibling
# via insertSiblingAfter, but the cursor was then placed via rowIndexOf which
# returned the first occurrence of that node in the rows list — the copy in the
# original source subtree above the mirror — rather than the copy shown through
# the mirror. The cursor jumped away from the mirror view.
#
# Fix: findRow(it, ctx) prefers the row where ctx matches the mirror the cursor
# was working in, keeping the cursor local to the mirror's displayed subtree.
#
# Repro (condensed):
#   Build src > a, create a mirror of src, navigate to a shown through
#   the mirror, press Enter (creates sibling M inside src), type M.
#   Expected: cursor stays on M as rendered through the mirror (not at the
#   original src > M row above the mirror header).
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

setup; launch

# Build: src > a
type "src"
send Enter         # cursor moves to empty sibling below src
type "a"
send Tab           # a becomes first child of src; cursor on a

wait_for "╰─ ○ a"
assert_contains "○ src"

# Create an empty sibling of src that will become the mirror.
# From a (child of src), press Enter -> empty sibling of a (still inside src),
# then Shift+Tab -> outdent to sibling of src.
send Enter         # empty node as sibling of a, inside src
send BTab          # Shift+Tab: outdent to sibling of src

wait_for "○ src"
# Three rows: src, a (child), empty sibling of src
assert_contains "╰─ ○ a"

# Save so the finder's DB search can see src and a.
send C-s
wait_for "3/3"

# Open the /mirror finder on the empty node (cursor is still there).
# Typing "/" enters slash-menu mode; "mirror" filters to /mirror.
type "/"
wait_for "/mirror"
type "mirror"
wait_for "/mirror"
send Enter         # run /mirror -> opens the node finder

# Finder opens with empty query showing all saved nodes; src appears first.
wait_for "src"
# Type "src" to narrow the finder to exactly the src node.
type "src"
wait_for "src"
send Enter         # select src -> empty node becomes ◆ src · mirror

# Mirror is created; cursor is on the mirror node.
wait_for "◆ src · mirror"
assert_contains "◆ a"

# Navigate DOWN from the mirror header (◆ src · mirror) into the child
# shown through the mirror (◆ a).  This is the node we will edit through
# the mirror to trigger the cursor-locality bug.
send Down

# Confirm cursor is on ◆ a (shown through the mirror, after the mirror row).
wait_for "4/4"   # 4 rows: src, a-orig, mirror-header, a-through-mirror

# Press Enter while cursor is on ◆ a (shown through the mirror).
# This creates a sibling of a inside the src subtree.
# BUG (pre-fix): cursor jumped to the new node's row in the ORIGINAL src
#   subtree (e.g. position 2/6), above the ◆ src · mirror line.
# FIXED:  cursor stays at the new node's row inside the mirror view
#   (position 5/6), below the ◆ src · mirror line.
send Enter
wait_for "/6"      # structural edit settled: total is now 6 rows (new empty node)
                   # position-agnostic so it settles in both buggy and fixed builds

# Type "M" to name the new node.
type "M"

wait_for "◆ M"          # M must appear through the mirror
assert_contains "○ M"   # M also appears in the original src subtree
assert_contains "◆ src · mirror"

# --- Core regression assertion ---
# The rows after the operation are:
#   1: ○ src
#   2: ├─ ○ M          <- original src child (cursor here in BUGGY build)
#   3: ╰─ ○ a
#   4: ◆ src · mirror
#   5: ├─ ◆ M          <- through mirror   (cursor here in FIXED build)
#   6: ╰─ ◆ a
#
# Status bar shows "pos/total". Assert the cursor is NOT at the original
# src position (2/6), which is where the pre-fix code would have landed.
assert_not_contains "· 2/6"

# Also confirm cursor IS within the mirror view: position 5/6.
assert_contains "5/6"

# Sanity: no crash.
assert_no_crash
pass
