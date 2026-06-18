#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: zooming into a mirror (alt+right) must show the source node's
# children, not an empty view. Before the fix (8f70a31) the zoom pushed the
# mirror item itself onto the viewStack; because a mirror carries no children
# in memory the zoomed view rendered blank. The fix resolves a mirror to its
# source before pushing the stack, so the source's live subtree appears.
#
# Scenario (mirror is a CHILD of src, sibling of kid):
#   src
#   ├─ kid             <- real child of src
#   ╰─ ◆ src · mirror  <- mirror of src (created via /mirror finder)
#
# Steps:
#   1. Build src + kid (kid is indented under src).
#   2. Save (ctrl+s) so the DB has src for the finder to find.
#   3. Add a blank sibling of kid (still a child of src) and /mirror src into it.
#   4. With the cursor on the mirror, zoom in with alt+right.
#   5. Assert the ZOOMED view shows the source's child:
#        - the breadcrumb becomes "root › src" (we descended into src), AND
#        - "kid" renders as a top-level row ("○ kid" with no tree prefix),
#          which is the source's child surfacing through the resolved mirror.
#      The empty-view regression would still flip the breadcrumb but show NO
#      "kid" row, so the presence of kid AFTER zoom is the load-bearing check.

setup; launch

# --- Step 1: build src > kid ----------------------------------------------
# After launch the cursor is on the single blank root node.
type "src"
send Enter            # open a blank sibling below src
type "kid"
send Tab              # indent: kid becomes the (only) child of src

# Sole child renders with the "╰─" prefix; this also proves kid is INDENTED
# under src rather than a root sibling.
wait_for "╰─ ○ kid"
assert_contains "○ src"

# --- Step 2: save so the finder can locate src in the DB -------------------
send C-s
wait_for "2/2" 5

# --- Step 3: mirror src into a new child of src ----------------------------
# Cursor is on kid. Enter opens a blank sibling of kid (still a child of src);
# we do NOT outdent it, so the mirror lives at src > <mirror>.
send Enter
type "/"
wait_for "/mirror"
type "mirror"
wait_for "/mirror"
send Enter            # run /mirror -> opens the node finder

# Finder is open. Narrow to src and select it.
wait_for "src"
type "src"
wait_for "src"
send Enter

# The blank node is now a mirror of src.
wait_for "◆ src · mirror"

# Pre-zoom sanity: we are at the top of the outline (breadcrumb has no "›"),
# src is a visible row, and kid is indented under it (kid is now the FIRST of
# two children, so it carries the "├─" prefix).
assert_contains "○ src"
assert_contains "├─ ○ kid"
assert_not_contains "root › "

# --- Step 4: zoom into the mirror -----------------------------------------
# Cursor is on the mirror node (◆ src · mirror). alt+right zooms in.
send M-Right

# --- Step 5: assert the zoomed view shows the source's child --------------
# The breadcrumb must show we descended into the source node: "root › src".
# This proves the zoom resolved the mirror to its source (not to the empty
# mirror reference) and actually changed the view root.
wait_for "root › src"

# The load-bearing assertion: the source's child "kid" must render in the
# zoomed pane. Before the fix the pane was EMPTY here. As the new view root's
# child, kid renders at depth 0 with the plain "○ kid" glyph and NO tree
# prefix, distinguishing it from the pre-zoom "╰─ ○ kid".
assert_contains "○ kid"

# The old view root (src) is now the hidden zoom root, so it must NOT appear
# as a body row, and kid must NOT carry its old child tree-prefix.
assert_not_contains "○ src"
assert_not_contains "╰─ ○ kid"

assert_no_crash
pass
