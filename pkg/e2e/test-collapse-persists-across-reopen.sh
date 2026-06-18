#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: collapsed fold state must survive a relaunch.
#
# Bug (pre 0b2af6f): collapse was local view-state that was never written to the
# DB, so every relaunch started with all nodes expanded regardless of what the
# user had folded.
#
# Fix (0b2af6f): each collapse/expand immediately calls persistCollapsed() which
# writes the fold via database.SetCollapsed (schema 19 local-only column); new
# nodes also carry their collapsed flag through Insert on save. On load, the
# collapsed column is read back into item.collapsed, restoring the fold.
#
# Repro:
#   1. use_persist_db so the DB is a temp file and survives reopen.
#   2. Type "parent", Enter.
#   3. Type "child", Tab (indent child under parent), Enter.
#   4. Move cursor Up onto the parent.
#   5. Press M-Up (alt+up) to collapse the parent; child disappears.
#   6. Save with C-s; reopen.
#
# Expected (post-fix):
#   After reopen:
#     - "parent" is still visible, rendered as the collapsed glyph (●).
#     - "child" is NOT visible (hidden inside the collapsed parent).
#     - The suffix "· 1 child" confirms the fold is live.

setup
use_persist_db
launch

# --- build the scenario ---

type "parent"
send Enter
# Enter must finish creating the new empty node before we type "child": wait for
# the 2-node count, NOT "○ parent" (which is also a substring of "○ parentchild"
# if "child" lands on the parent node while Enter is still in flight).
wait_for "○ parent"
wait_for "2/2"

type "child"
send Tab
wait_for "╰─"

# Confirm dirty state before moving the cursor.
wait_for "unsaved"

# Move cursor back up onto the parent.
send Up
wait_for "○ parent"

# Collapse the parent (alt+up = M-Up in tmux).
send M-Up
# The child should now be hidden; the parent should show the collapsed glyph.
wait_for "● parent"

# Double-check the child row is gone before saving.
# A visible child row renders with the tree prefix "╰─" or a bullet "○ child".
assert_not_contains "╰─"
assert_contains "● parent"

# Save so the node names (and the collapse flag as a backstop) are written to
# the file DB. C-s is synchronous within the editor's event loop; the 1.2s
# settle that reopen() imposes before reasserting gives the write plenty of
# time to land.
send C-s

# --- reopen and assert ---

reopen

# The outline must load with the parent still collapsed.
wait_for "● parent" 8

# Core assertion: the parent still shows the collapsed (filled) glyph.
assert_contains "● parent"

# The child must still be hidden — the child ROW (with tree connector prefix)
# must not appear. The word "child" appears only in the "· 1 child" suffix on
# the collapsed parent; the actual row would show "╰─" or "○ child" as a
# separate line.
assert_not_contains "╰─"

# The suffix "· 1 child" on the collapsed parent confirms the fold is live.
assert_contains "1 child"

assert_no_crash
pass
