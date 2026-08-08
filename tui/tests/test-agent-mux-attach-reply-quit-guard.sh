#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# The agent-chip multiplexer, end to end against a FAKE pi CLI: ⌥r resumes the
# session on a background PTY and opens the attach view (ctrl+q detaches, the
# agent keeps running, the toolbar carries the red tally), ⌥m's quick-reply box
# ships a message the agent actually receives, and quitting with a live agent
# warns once before the second press stops it and goes.

setup

# the session record the picker files the chip from (pi's store layout)
id="019eb6ff-caef-78d0-8a2a-93e6ddd1f826"
sessions="${TEST_HOME}/.pi/agent/sessions/--home-dev-repo--"
mkdir -p "${sessions}"
cat > "${sessions}/2026-06-11T14-04-37-487Z_${id}.jsonl" <<JSON
{"type":"session","version":3,"id":"${id}","cwd":"${TEST_HOME}"}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":"ride the mux"}]}}
{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"saddled up"}]}}
JSON

# the fake CLI: paints titles the way an agent TUI does (braille spinner while
# working, ✳ at rest), then echoes whatever the reply box sends it
mkdir -p "${TEST_HOME}/bin"
cat > "${TEST_HOME}/bin/pi" <<'FAKE'
#!/usr/bin/env bash
printf '\033]0;\342\240\271 pondering\007'
echo "pi resumed with: $*"
echo "Working..."
sleep 1.2
printf '\033[2J\033[H'
printf '\033]0;\342\234\263 pondered the repo\007'
echo "ready for input"
while IFS= read -r line; do
  printf '\033]0;\342\240\271 replying\007'
  echo "Working..."
  sleep 0.8
  printf '\033[2J\033[H'
  printf '\033]0;\342\234\263 replied\007'
  echo "you said: ${line}"
done
sleep 600
FAKE
chmod +x "${TEST_HOME}/bin/pi"

# launch with the fake CLI on PATH (lib's launch does not carry PATH)
tmux new-session -d -s "${SESSION}" -x "${WIN_W}" -y "${WIN_H}" \
    "HOME='${TEST_HOME}' PATH='${TEST_HOME}/bin:${PATH}' XDG_CONFIG_HOME='${TEST_HOME}' XDG_DATA_HOME='${TEST_HOME}' XDG_CACHE_HOME='${TEST_HOME}' LFLOW_NO_DAEMON=1 TERM=xterm-256color '${BIN}' node open"
sleep "${SETTLE_AFTER_LAUNCH}"

# file the chip from the store
type "/"
type "insert"
send Enter
type "agent"
send Enter
wait_for "ride the mux" 8
send Enter
wait_for "ᴘɪ ride the mux" 8

# ⌥r: resume on the mux, attach view up, the fake CLI on its PTY
send M-r
wait_for "ctrl+q detach" 8
wait_for "pi resumed with: --session-id ${id}" 8

# ctrl+q: back to the outline, agent still running, tallied in the toolbar
send C-q
wait_for "detached · the agent" 5
wait_for "1 agent" 5
assert_contains "ᴘɪ ride the mux"

# ⌥m: the quick-reply box, message shipped to the live PTY
send M-m
wait_for "enter send · esc close" 5
type "hello mux"
send Enter
wait_for "sent" 5
send Escape

# re-attach: the agent actually received the reply
send M-r
wait_for "you said: hello mux" 8
send C-q
wait_for "1 agent" 5

# quit guard: first press warns, second stops the agent and goes
send C-q
wait_for "quit again" 5
assert_no_crash
send C-q
deadline=$(( $(date +%s) + 8 ))
while tmux has-session -t "${SESSION}" 2>/dev/null; do
    if (( $(date +%s) > deadline )); then fail "editor did not quit on the second press"; fi
    sleep 0.1
done
pass
