#!/usr/bin/env bash
# test-voice-nil-map-panic-on-load.sh
#
# Regression: voiceRender wrote to the nil m.voiceDur map before the nil-check
# (which only covered voiceEnv) when a voice node was loaded from disk and
# runVoice had never initialized the lazy-load maps. On reopen the maps are nil,
# and rendering the voice node row panicked with "assignment to entry in nil map".
#
# Repro:
#   1. Create a node, set its type to Voice, save.
#   2. Place a minimal file at the voice wav path so fileExists() returns true
#      on reopen (forcing the lazy-load branch to run).
#   3. Kill + relaunch against the same persistent DB (maps are nil again).
#   4. Assert the voice row renders without any panic / nil map / goroutine dump.

set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Use a persistent file DB so data survives the reopen.
setup
use_persist_db

# Grab the DB path now (it was written by use_persist_db).
DB_PATH="${TEST_HOME}/.lflow/persist.db"

launch

# Build the scenario: one node named "meeting", then set its type to Voice.
type "meeting"
wait_for "○ meeting"

# Open the slash menu by sending "/" as a key press.
# The slash opens the picker inline; "type" filters it to just "/type".
send /
# The slash menu is a bounded scrolling list; type the query to filter to /type.
# Filter to "/type" by typing the query letters one at a time so each
# 40ms key gap lets the pane re-render before the next character.
send t
send y
send p
send e
wait_for "/type"
send Enter

# The /type picker is now open. Type "voice" to filter; only Voice matches.
wait_for "type to search"
send v
send o
send i
send c
send e
wait_for "Voice"
send Enter

# The voice node now renders its empty-state label even before saving. Confirm
# the type actually flipped to Voice (renderM replaces the body, so the name
# "meeting" is gone and the empty-state label is shown instead).
wait_for "empty" 5
assert_contains "empty"

# Save to the persistent DB. NOTE: the "empty" label is present BEFORE the save
# too, so it cannot gate the commit. The save is async; poll the DB itself until
# the voice node is actually written, otherwise the kill below can race ahead of
# the flush and the uuid lookup finds nothing.
send C-s
VOICE_UUID=""
_save_deadline=$(( $(date +%s%N) + 5 * 1000000000 ))
while :; do
    VOICE_UUID="$(sqlite3 "${DB_PATH}" "SELECT uuid FROM nodes WHERE type='voice' AND deleted=0 LIMIT 1;" 2>/dev/null || true)"
    [[ -n "${VOICE_UUID}" ]] && break
    if (( $(date +%s%N) > _save_deadline )); then
        LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
        fail "voice node never committed to DB at ${DB_PATH} after C-s"
    fi
    sleep 0.08
done

# Kill the session (but do NOT call reopen yet — we need to plant the wav first).
tmux kill-session -t "${SESSION}" 2>/dev/null || true
SESSION="${SESSION}_r"

# Write a stub file at the voice wav path. The file just needs to exist so that
# fileExists() in voiceRender returns true, triggering the lazy-load branch
# that previously panicked by writing to a nil m.voiceDur map.
VOICE_WAV="${TEST_HOME}/lflow/voice/${VOICE_UUID}.wav"
mkdir -p "$(dirname "${VOICE_WAV}")"
printf '\x00\x00\x00\x00' > "${VOICE_WAV}"

# Relaunch against the same HOME+DB (voiceEnv/voiceDur maps start nil).
launch

# Wait for the voice node row to paint. After the fix voiceRender initialises
# both maps before writing, so the render succeeds and shows either the
# waveform or the empty-state "▸ empty · ⌥r record" label.
wait_for "▸" 8

# The voice row must display without any crash — this is the specific assertion
# that would have caught the nil-map panic on the original buggy binary.
assert_no_crash

# Also assert the voice empty-state label is visible. After the fix, a stub
# wav that parseWavEnvelope cannot parse produces an empty envelope, which
# voiceRender turns into "▸ empty · ⌥r record". A panic would prevent this.
assert_contains "empty"
assert_not_contains "nil map"
assert_not_contains "assignment to entry"

pass
