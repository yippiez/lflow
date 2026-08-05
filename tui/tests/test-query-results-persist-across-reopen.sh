#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: query result mirrors must survive a relaunch.
#
# Bug (pre 9d4b002): result mirrors were created with the ephemeral 'derived'
# flag which caused save to skip them. On every relaunch the query node showed
# zero children — the results had not been written to the DB.
#
# Fix (9d4b002): mirrors are now REAL persisted nodes reconciled in place on
# each run, so they are written to the DB on save and restored on reopen.
#
# Repro:
#   1. use_persist_db so the DB is a temp file (not :memory:) and survives reopen.
#   2. Create an ordinary note: "target note".
#   3. Create a blank sibling, change its type to Query via the /type picker.
#   4. Type "target" as the query text.
#   5. Press M-r to run the query; wait for the mirror child to appear.
#   6. Save with C-s (confirmed by disappearance of "unsaved" from the status bar).
#   7. Reopen.
#
# Expected (post-fix):
#   After reopen the query node shows "⌕ target · 1 hits" and its mirror child
#   "○ target note · mirror · fixed" — proving the result was persisted to the DB and
#   loaded back on restart.

# Use a persistent file DB so the reopen sees the same data.
setup
use_persist_db
launch

# --- step 1: create the target note ---
type "target note"
send Enter

# We are now on a blank sibling node below "target note".
wait_for "○ target note"

# --- step 2: convert the blank node to a Query node via /type ---
type "/"
type "type"
send Enter

# /type picker is open — filter to "query" and confirm.
type "query"
send Enter

# The blank node is now a query node (⌕ prefix).
wait_for "⌕"

# --- step 3: type the query text ---
type "target"
wait_for "⌕ target"

# --- step 4: run the query ---
send M-r

# Wait for the mirror child to appear. The query is an in-memory search so it
# is fast, but give it a generous budget.
wait_for "○ target note · mirror · fixed" 10

# Confirm the fixed mirror child rendered before we save.
assert_contains "○ target note · mirror · fixed"
assert_contains "mirror"
assert_contains "hits"

# --- step 5: save so results are persisted to the file DB ---
# Save is confirmed by the "unsaved" marker disappearing from the status bar.
send C-s
wait_for "⌕ target" 5
# After save the status bar drops "· unsaved"; poll until it is gone.
DEADLINE=$(( $(date +%s) + 5 ))
while :; do
    LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
    if [[ "${LAST_PANE}" != *"unsaved"* ]]; then
        break
    fi
    if (( $(date +%s) > DEADLINE )); then
        fail "timed out waiting for save to complete (unsaved still in pane)"
    fi
    sleep 0.1
done

# --- step 6: reopen (kill + relaunch against the same file DB) ---
reopen

# The editor must load cleanly with the query node visible.
wait_for "⌕ target" 8

# Core assertion: the fixed mirror child survived the relaunch.
# Before the fix (9d4b002) derived mirrors were not saved, so this would be
# absent after reopen, leaving the query node with "0 hits" and no children.
assert_contains "○ target note · mirror · fixed"
assert_contains "mirror"

# The query node must report at least 1 hit after reopen.
assert_contains "hits"

# The "target note" plain node also survived (sanity check on basic persistence).
assert_contains "○ target note"

assert_no_crash
pass
