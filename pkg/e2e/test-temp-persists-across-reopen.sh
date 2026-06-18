#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Behavior: the Temporary Domain now PERSISTS (7-day retention) — it is no longer
# session-ephemeral.
#
# Design (supersedes the old db=nil temp tree): the notes db has two top-level
# roots, "root" and "temp". Temp content is the subtree under the "temp" root —
# a normal persisted, synced subtree. A startup sweep deletes temp entries
# unchanged for 7 days, but anything recent survives a relaunch.
#
# Repro:
#   1. use_persist_db so the file DB survives the relaunch.
#   2. Launch; type "keepme" in the main outline.
#   3. Press Down — focus drops into the Temporary Domain panel.
#   4. Type "scratchy" in the temp panel.
#   5. Ctrl-S, then reopen.
#
# Expected (correct post-fix behavior):
#   - After reopen the temp node "scratchy" STILL renders as "◌ scratchy".
#   - The persistent DB has "scratchy" parented under the "temp" root.
#   - The main-outline node "keepme" also survived.

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

command -v sqlite3 >/dev/null 2>&1 || { echo "SKIP ${TEST_NAME}: sqlite3 not installed"; exit 0; }

setup
use_persist_db
launch

# --- step 1: main-outline node ---
type "keepme"
wait_for "○ keepme"

# --- step 2: drop into the Temporary Domain ---
send Down
wait_for "◌"

# --- step 3: name the temp node ---
type "scratchy"
wait_for "◌ scratchy"

assert_contains "○ keepme"
assert_contains "◌ scratchy"

# --- step 4: persist and reopen ---
send C-s
sleep 0.4
assert_no_crash

reopen

# --- assertions after reopen: temp content SURVIVES ---
wait_for "◌ scratchy" 8
assert_contains "◌ scratchy"        # temp node re-rendered from the DB
assert_contains "○ keepme"          # main outline survived too

# DB level: the temp node is persisted under the "temp" root.
assert_db_has "name = 'scratchy' AND parent_uuid = 'temp' AND deleted = 0" "the persisted temp node under the temp root"
assert_db_has "name = 'keepme'" "the main-outline node 'keepme'"

assert_no_crash
pass
