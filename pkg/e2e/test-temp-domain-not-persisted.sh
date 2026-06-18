#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: Temporary Domain nodes must NEVER be persisted.
#
# Bug (pre 49e7a7d): The Temporary Domain is a SECOND tree with a nil db, so
# tree.save() is supposed to be a no-op for it. If save() does NOT guard a nil
# db (or the temp tree is ever wired to a real db), then pressing Ctrl-S while
# focused in the temp panel writes the scratch nodes into the persistent DB.
# They then survive across relaunches — violating the "ephemeral" invariant.
#
# Fix (49e7a7d): tree.save() short-circuits when db == nil, so the temp tree's
# save() is a true no-op. The main-outline tree (real db) is unaffected.
#
# Why this test checks the DATABASE and not just the rendered pane:
#   A leaked temp node is parented under the synthetic "temp-root" uuid, which
#   is NOT in the main outline's loaded view, and the temp panel is always
#   rebuilt empty in-memory on launch. So a leaked node would NOT re-render on
#   screen even though it is sitting in the persistent DB. The only place the
#   regression is observable is the DB itself — so we assert there.
#
# Repro (a plausible user action — hitting Ctrl-S out of habit in the panel):
#   1. use_persist_db so the file DB survives the relaunch.
#   2. Launch; type "keepme" in the main outline; Ctrl-S to persist it.
#   3. Press Down — focus drops into the Temporary Domain panel.
#   4. Type "ephemeral" — names the temp cursor node.
#   5. Ctrl-S while STILL in the temp panel — this is the call that must be a
#      no-op for the temp tree.
#   6. Reopen.
#
# Expected (correct post-fix behavior):
#   - Rendered pane after reopen shows "○ keepme" (main outline survived).
#   - Persistent DB contains a node named "keepme".
#   - Persistent DB contains NO node named "ephemeral" and NO node parented
#     under "temp-root" (the temp tree was never persisted).

# db_count "<sql-where>": echo how many `nodes` rows match. Uses the persistent
# file DB that use_persist_db created under the isolated HOME.
db_count() {
    local where="$1"
    local dbfile="${TEST_HOME}/.lflow/persist.db"
    if [[ ! -f "${dbfile}" ]]; then
        fail "persistent DB not found at ${dbfile}"
    fi
    sqlite3 "${dbfile}" "SELECT COUNT(*) FROM nodes WHERE ${where};"
}

assert_db_has() {
    local where="$1" desc="$2"
    local n; n="$(db_count "${where}")"
    if [[ "${n}" -lt 1 ]]; then
        fail "expected DB to contain ${desc} (where: ${where}) but found 0 rows"
    fi
}

assert_db_lacks() {
    local where="$1" desc="$2"
    local n; n="$(db_count "${where}")"
    if [[ "${n}" -ne 0 ]]; then
        fail "expected DB to NOT contain ${desc} (where: ${where}) but found ${n} rows — temp content leaked into the persistent DB"
    fi
}

command -v sqlite3 >/dev/null 2>&1 || { echo "SKIP ${TEST_NAME}: sqlite3 not installed"; exit 0; }

setup
use_persist_db
launch

# --- step 1: create + persist the main-outline node ---
type "keepme"
wait_for "○ keepme"
send C-s
sleep 0.3

# --- step 2: navigate Down into the Temporary Domain panel ---
send Down
wait_for "◌"

# --- step 3: type the temp node name ---
type "ephemeral"
wait_for "◌ ephemeral"

# Sanity: both regions are visible before we save.
assert_contains "○ keepme"
assert_contains "◌ ephemeral"

# --- step 4: Ctrl-S while STILL inside the temp domain ---
# This is the load-bearing keystroke: it calls save() on the temp tree, which
# MUST be a no-op (nil db). If the nil-db guard regresses, "ephemeral" is
# written into the persistent DB here.
send C-s
sleep 0.3

# The editor MUST still be alive and rendering after that Ctrl-S. If the nil-db
# guard regresses to an UNGUARDED nil db, save() panics here and the process
# dies — so the pane vanishes and a later post-reopen check would be fooled into
# passing on stale DB state. Catch that crash now, while it is observable.
if ! tmux capture-pane -t "${SESSION}" -p >/dev/null 2>&1; then
    fail "editor died after Ctrl-S in the Temporary Domain (likely a save() panic on the nil-db temp tree)"
fi
assert_no_crash
wait_for "◌ ephemeral" 5   # pane is still up and still showing our temp node

reopen

# --- assertions after reopen ---

# The main-outline node must still render and must be in the DB.
wait_for "○ keepme" 8
assert_contains "○ keepme"
assert_db_has "name = 'keepme'" "the main-outline node 'keepme'"

# Core regression assertion (DB level — see header): the temp node must NOT
# have been persisted, neither by name nor as a child of the temp root.
assert_db_lacks "name = 'ephemeral'" "the temp node 'ephemeral'"
assert_db_lacks "parent_uuid = 'temp-root'" "any node under the temp root"

# The Temporary Domain panel is always visible but rebuilt empty.
assert_contains "◌"

assert_no_crash
pass
