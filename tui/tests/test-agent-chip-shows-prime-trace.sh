#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# A prime-agent transcript lives at ~/.prime/agent/sessions/<uuid>.jsonl — the
# file is named with just the session id, no timestamp prefix (that is a pi
# convention). Filing it onto a session CHIP must immediately open the trace
# rather than showing an empty chip with "no readable records".

setup
id="019fd6c9-67cd-770f-8aff-a329566b2f4d"
sessions="${TEST_HOME}/.prime/agent/sessions"
mkdir -p "${sessions}"
cat > "${sessions}/${id}.jsonl" <<JSON
{"type":"session","version":3,"id":"${id}","cwd":"/home/dev/repo","rlmDepth":0}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":"show the real RLM trace"}]}}
{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"here is the assistant text reply"}]}}
{"type":"session_info","name":"nearbysend Worker 1"}
JSON
launch

type "/"
type "insert"
send Enter
type "agent"
send Enter
wait_for "nearbysend Worker 1" 8
# /insert → Agent opens an asynchronously filled session picker; select only
# after the fake prime-agent transcript has appeared in it.
send Enter
wait_for "RLM nearbysend Worker 1" 8
# the chip is filed; ⌥e opens its trace panel
send M-e
wait_for "traces · 2 events" 8
assert_contains "here is the assistant text reply"
assert_not_contains "no readable records"
assert_no_crash
pass
