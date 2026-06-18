#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: escaping the slash menu left a stray '/' in the node text and
# could wipe the status bar.
#
# Bug (pre-eda6004): pressing Escape (or Backspace) to dismiss the slash menu
# left the triggering '/' character in the node name (e.g. "hello/" or
# "hello/q") and blanked the status bar for a frame or entirely.
#
# Fix (eda6004): on esc, stripSlashText() removes the slash and any typed
# query so the node name reverts to exactly what it was before the menu opened.
# The status bar rendering was already fixed to sit below the command list so it
# always survives menu dismissal.
#
# Repro steps (from the bug report):
#   1. Launch with an empty :memory: outline.
#   2. Type "hello" to create a node.
#   3. Type "/" to open the slash menu.
#   4. Send Escape to dismiss the menu.
#
# Expected correct (post-fix) behavior:
#   - The node text is exactly "hello" — no trailing slash.
#   - A literal "/" must NOT appear in the node text.
#   - The status bar is visible (contains the position indicator e.g. "1/1").
#   - No crash.

setup; launch

# Step 1: type "hello" to create the first node.
type "hello"

# Synchronize: wait until the rendered outline shows the node.
wait_for "○ hello"

# Confirm no stray slash is present before the repro.
assert_not_contains "○ hello/"

# Step 2: type "/" to open the slash command menu.
type "/"

# The slash menu should now be open. Wait for its first entry "/complete" to
# appear (the bounded list always shows it; "/type" scrolls in only on filter).
wait_for "/complete"

# Step 3: press Escape to dismiss the menu without choosing a command.
send Escape

# Give the editor a moment to repaint.
sleep 0.1

# --- Assertions: correct post-fix behavior ---

# The node text must be exactly "hello" — the triggering slash must be gone.
assert_contains "○ hello"

# No literal slash must appear appended to "hello" in any form.
assert_not_contains "○ hello/"
assert_not_contains "hello /"

# The status bar must be visible. With one node it shows "1/1".
assert_contains "1/1"

assert_no_crash
pass
