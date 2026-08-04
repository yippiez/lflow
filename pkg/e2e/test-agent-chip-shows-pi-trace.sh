#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# A Pi transcript filename carries timestamp_<session-id>.jsonl. Filing it onto a
# session CHIP must immediately open the trace rather than showing an empty chip
# with "no readable records". (The whole-row Agent NODE was retired 2026-08-04 —
# the chip is a session's one surface — so this drives the chip.)

setup
id="019eb6ff-caef-78d0-8a2a-93e6ddd1f826"
sessions="${TEST_HOME}/.pi/agent/sessions/--home-dev-repo--"
mkdir -p "${sessions}"
cat > "${sessions}/2026-06-11T14-04-37-487Z_${id}.jsonl" <<JSON
{"type":"session","version":3,"id":"${id}","cwd":"/home/dev/repo"}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":"show the real Pi trace"}]}}
{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","name":"bash","arguments":{"command":"go test ./..."}}]}}
JSON
launch

type "/"
type "insert"
send Enter
type "agent"
send Enter
wait_for "show the real Pi trace" 8
# /insert → Agent opens an asynchronously filled session picker; select only
# after the fake Pi transcript has appeared in it.
send Enter
wait_for "ᴘɪ show the real Pi trace" 8
# the chip is filed; ⌥e opens its trace panel
send M-e
wait_for "traces · 2 events" 8
assert_contains "traces · 2 events"
assert_contains "go test ./..."
assert_not_contains "no readable records"
assert_no_crash
pass
