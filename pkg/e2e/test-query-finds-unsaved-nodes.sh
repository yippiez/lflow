#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: query must find nodes that are only in memory (never saved to the DB).
#
# Bug: before the fix the query runner called database.SearchNodes only, which
# queries the FTS index. A node typed in the current session but not yet persisted
# was never in the FTS index, so it was never returned. The fix (9d4b002) adds an
# in-memory scan pass in queryMatches that walks m.tree.byUUID, so brand-new
# unsaved nodes are also surfaced.
#
# Repro (DB is :memory: so NOTHING is ever persisted):
#   1. Type "freshunsaved" and press Enter — this node lives only in memory.
#   2. On the blank node that follows, open the /type picker, select Query.
#   3. Type "freshunsaved" as the query text.
#   4. Press M-r (alt+r) to run.
#
# Expected (correct, post-fix) behavior:
#   - The query node shows "⌕ G freshunsaved" with "· 1 hits" in the suffix.
#   - A fixed mirror child "○ freshunsaved · mirror · fixed" appears under it.
#   - The assertion WOULD HAVE FAILED on the original code (0 hits, no mirror).

setup; launch

# --- step 1: type the unsaved node ---
type "freshunsaved"
send Enter

# We are now on the blank node that follows "freshunsaved".
wait_for "○ freshunsaved"

# --- step 2: open the /type picker and choose Query ---
# Typing "/" opens the slash command menu.
type "/"
# Filter to /type.
type "type"
send Enter

# The type picker is open. Filter to "query".
type "query"
send Enter

# The blank node is now a query node.
wait_for "⌕"

# --- step 3: type the query text ---
type "freshunsaved"

# The node should render as "⌕ G freshunsaved".
wait_for "⌕ G freshunsaved"

# --- step 4: run the query ---
send M-r

# Wait for "hits" to appear in the suffix.  The in-memory search is fast; 5s is plenty.
wait_for "hits" 5

# --- assertions: correct post-fix behavior ---

# The query node must show exactly 1 hit (the unsaved "freshunsaved" node).
assert_contains "1 hits"

# A fixed mirror child must appear — this is the in-memory match.
assert_contains "○ freshunsaved · mirror · fixed"

# The word "mirror" must appear in the suffix of that child row.
assert_contains "mirror"

# "0 hits" would mean the old (broken) code path ran — assert it is absent.
assert_not_contains "0 hits"

assert_no_crash
pass
