#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: a query node with a syntax error (e.g. "(unclosed" or
# "foo :AND :OR", which are invalid ripgrep regex patterns) must NOT silently
# show "no matches" and must NOT crash the editor. Instead it must continue
# functioning and show a proper result state (N hits).
#
# Bug (pre-fix, b2aea1a): the query runner shelled out to `rg --json`. When the
# user typed an invalid regex pattern such as "(unclosed", rg exited with code 1
# and empty stdout; the handler fell through to the silent fallback:
#   return queryDoneMsg{uuid, []outLine{{text: "no matches"}}}
# so the error was swallowed and displayed as a benign placeholder. There was
# no red error state and no indication that the query failed rather than simply
# matching nothing.
#
# Fix (c5f9957): the query engine was replaced with database.SearchNodes over
# the user's notes, using LIKE and FTS5. Neither path can produce a regex syntax
# error from user input, so a "malformed" pattern like "(unclosed" is treated as
# a literal search string and either finds matches or correctly returns 0 hits.
# The editor keeps running; the ⌕ node remains visible with its suffix.
#
# Repro steps (exact, matching the bug report):
#   1. setup; launch — blank in-memory outline
#   2. on a blank node make it a query node via /type picker
#   3. type a malformed query "(unclosed"
#   4. press M-r to run
#
# Expected (correct, post-fix) behavior to assert:
#   - The ⌕ query node is still visible (editor did not crash/vanish)
#   - The suffix shows "N hits" (query executed, even if it found nothing)
#   - The old silent-failure placeholder "no matches" does NOT appear
#   - assert_no_crash confirms no Go panic / runtime error / goroutine dump

setup; launch

# We start on the single blank root-level node. Open the /type picker to make
# it a query node.
type "/"
type "type"
send Enter

# The /type picker is open. Filter to "Query" and select it.
type "query"
send Enter

# The node is now a query node; the ⌕ sign should appear.
wait_for "⌕"

# Type the malformed query: "(unclosed" is an invalid ripgrep regex pattern
# that triggered the silent-failure path in the original code.
type "(unclosed"

# Confirm the query text is rendered in the node name.
wait_for "⌕ (unclosed"

# Press M-r (alt+r) to run the query. In the fixed code this searches the DB
# with LIKE / FTS5 — no regex involved, no syntax error possible. The query
# runs synchronously (no background goroutine); results appear immediately.
send M-r

# Wait for the post-run marker. A query node shows "N hits" UNCONDITIONALLY
# (even before it is ever run), so "hits" alone does not prove the query ran.
# The "updated <relative time>" suffix is driven by queryRunAt, which is set
# ONLY inside runQuery — so it appears only after M-r actually executed the
# query path. Waiting for it proves the alt+r run happened without crashing.
wait_for "updated" 5

# --- assertions: correct (post-fix) behavior ---

# The ⌕ glyph must still be present — the node did not vanish or crash.
assert_contains "⌕"

# The query text must still be intact under the glyph (node not corrupted).
assert_contains "⌕ (unclosed"

# The suffix must contain "hits" — the query engine ran and produced a count,
# not the old "no matches" placeholder that hid the error.
assert_contains "hits"

# "updated" proves the query actually RAN (queryRunAt set in runQuery). If M-r
# handling regressed and the query never executed on the malformed input, the
# unconditional "0 hits" would still show but "updated" would NOT — so this is
# the assertion that fails if the bad-syntax run silently does nothing.
assert_contains "updated"

# The old silent-failure text must NOT appear. Its presence means the
# pre-fix ripgrep code path ran (catching invalid-regex exit-1 as "no matches").
assert_not_contains "no matches"

# No Go panic or runtime error.
assert_no_crash

pass
