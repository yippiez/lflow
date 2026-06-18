#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: the query node (⌕) must search the notes DB and mirror matching
# notes as read-only children — NOT run ripgrep over the codebase.
#
# Bug: the original implementation called `rg --json` and showed file paths as
# its children. The fix switched it to database.SearchNodes over the user's notes
# and reconciles MIRROR children (◆).
#
# Fix commit: c5f9957
#
# Repro:
#   1. Create two ordinary note nodes: "alpha note" and "beta note".
#   2. Press Enter on "beta note" to land on a fresh blank node.
#   3. Open the /type picker (type "/type", filter "query", Enter) to make the
#      blank node a query node.
#   4. Type "alpha" as the query text.
#   5. Press M-r (alt+r) to run the query.
#
# Expected (correct, post-fix) behavior:
#   - The query node shows "⌕ alpha" with "· 1 hits" in the suffix.
#   - A mirror child "◆ alpha note · mirror" appears under it.
#   - No file paths or ripgrep-style output (e.g., ".go:" or ".sh:") appear.

setup; launch

# --- step 1: create two ordinary note nodes ---
type "alpha note"
send Enter
type "beta note"
send Enter

# We are now on a new blank node (sibling after "beta note").
wait_for "○ beta note"

# --- step 2: open the /type picker and select Query ---
# Typing "/" opens the slash command menu.
type "/"
# Filter to "/type"
type "type"
# Choose /type from the slash command list.
send Enter

# The /type picker is now open. Type "query" to filter to the Query type.
type "query"
# Select it.
send Enter

# The blank node is now a query node with an empty name (⌕ prefix).
# The /type picker closed and we are back in outline mode.
wait_for "⌕"

# --- step 3: type the query text ---
type "alpha"

# The query node should now read "⌕ alpha".
wait_for "⌕ alpha"

# --- step 4: run the query (alt+r) ---
send M-r

# Wait for the query to complete. The in-memory search is synchronous so
# results appear immediately; give it up to 5s.
wait_for "hits"

# --- assertions: correct (post-fix) behavior ---

# The query node must show the hit count suffix.
assert_contains "⌕ alpha"
assert_contains "hits"

# The mirror child for "alpha note" must appear — this is the DB-search result.
# A mirror renders as "◆ alpha note · mirror".
assert_contains "alpha note"
assert_contains "mirror"

# "beta note" does NOT match "alpha" — it must NOT appear as a mirror child.
assert_not_contains "◆ beta"

# Ripgrep-style output: file paths contain ":" — file matches look like
# "pkg/tui/...:42: ..." or similar. Assert none of those patterns appear.
# The negative check is: no line of the form  <path>.<ext>:<digits>
# (ripgrep match lines). We check for "no matches" (old error fallback) and
# for typical file extensions in match output.
assert_not_contains "no matches"
assert_not_contains ".go:"
assert_not_contains ".sh:"
assert_not_contains ".md:"

assert_no_crash
pass
