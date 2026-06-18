#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: bold/italic are per-node style attributes; asterisks must render
# literally in the node name and must not be consumed as inline markup markers.
#
# Bug (pre 4b3b3a5): the inline renderer parsed **bold** and *italic* markers,
# so a node named "a*b*c" would hide the asterisks and show only "abc" with
# italic styling — the characters were consumed as markup. Additionally, /style
# was not the only way to apply bold: typed asterisks could accidentally trigger
# inline bold rendering.
#
# Fix (4b3b3a5): inlineSpans no longer parses * or ** sequences. Asterisks in
# node text are plain rune characters and always appear literally in the
# rendered output. Text styling is exclusively a per-node attribute set via
# /style (/bold, /italic, /underline, /color), which sets item.style and is
# rendered by styleAttrs(), not by inline markup scanning.
#
# Repro:
#   1. setup; launch
#   2. type "a*b*c" → creates a node whose name contains literal asterisks
#   3. open /style and apply bold (type "/", then "style", then Enter to
#      select /style; the style picker opens with Bold first; press Enter to
#      toggle bold on)
#   4. assert the node name renders as "a*b*c" (all five characters present,
#      asterisks not eaten)
#   5. assert bold is active on the node (re-open /style; "Bold (on)" appears)
#
# Pre-fix failure: the node would render as "abc" with the * chars hidden.

setup; launch

# --- step 2: create a node with literal asterisks in the name ---
type "a*b*c"
wait_for "a*b*c"

# Verify the literal text is present before doing anything else.
assert_contains "a*b*c"

# Commit the node (Enter creates the next node; cursor moves down).
send Enter

# Move back up to "a*b*c" so /style targets it.
send Up

wait_for "a*b*c"

# --- step 3: open /style and apply bold ---

# Type "/" to open the slash menu (it is typed inline into the node text but
# stripped when a command runs).
type "/"
wait_for "/style"

# Type "style" to filter the menu to just /style.
type "style"
wait_for "/style"

# Press Enter to run /style — this opens the style picker.
send Enter
# The style picker's first item is "Bold"; wait for it to appear.
wait_for "Bold"

# Press Enter on "Bold" (the pre-selected first item) to toggle bold on.
send Enter

# Back in outline mode. The node "a*b*c" must still show its full literal text.
wait_for "a*b*c"

# --- step 4: assert asterisks render literally (the core regression check) ---
# Pre-fix: the node rendered as "abc" — two asterisks hidden as *italic* markers.
# Post-fix: all five characters "a*b*c" appear unchanged.
assert_contains "a*b*c"
assert_not_contains "○ abc"

# --- step 5: confirm bold was applied as a per-node attribute ---
# Re-open /style and verify "Bold (on)" appears in the picker — meaning the
# style attribute was set, not that asterisk markup triggered rendering.
type "/"
wait_for "/style"
type "style"
wait_for "/style"
send Enter
wait_for "Bold"
assert_contains "Bold"
assert_contains "(on)"

# Close the style picker.
send Escape

assert_no_crash
pass
