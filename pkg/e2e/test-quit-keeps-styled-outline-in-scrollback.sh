#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Regression: quitting the editor must keep the styled outline in scrollback.
#
# Bug (pre 6383a85): pressing ctrl+c erased the editor frame; View() returned
# "" on quit so bubbletea blanked the screen, losing the outline. ctrl+q did
# the same. The expected behavior is that the final bubbletea frame is the
# whole tree rendered with glyphs (○ …) so the outline survives in the
# terminal scrollback after the editor process exits.
#
# Fix (6383a85): View() now returns finalView() when m.quitting is true,
# rendering every tree row with glyphs and connectors as the last frame.
# Because the normal-screen cursor follows that final render, the outline
# stays visible in the terminal (and scrollback) after exit.
#
# Repro steps:
#   1. setup; launch
#   2. type "Hello"; send Enter  → first node
#   3. type "World"              → second node (cursor on "World")
#   4. quit the editor (send C-c)
#   5. snapshot the pane (including history) after the editor exits
#
# Expected (post-fix):
#   Both "○ Hello" and "○ World" appear in the pane/scrollback after quit.

# Helper: capture the full pane including history (-S - = from start of history).
# Needed because after lflow exits the final frame may be above the visible area.
scrollback_contains() {
    local sub="$1"
    LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p -S - 2>/dev/null || true)"
    if [[ "${LAST_PANE}" != *"${sub}"* ]]; then
        fail "expected scrollback to contain: ${sub}"
    fi
}

# Helper: poll the scrollback until <sub> appears or timeout (default 5s).
wait_for_scrollback() {
    local sub="$1"
    local timeout="${2:-5}"
    local deadline
    deadline=$(( $(date +%s%N) + timeout * 1000000000 ))
    while :; do
        LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p -S - 2>/dev/null || true)"
        if [[ "${LAST_PANE}" == *"${sub}"* ]]; then return 0; fi
        if (( $(date +%s%N) > deadline )); then
            fail "timed out waiting for scrollback to contain: ${sub}"
        fi
        sleep 0.08
    done
}

setup; launch

# --- step 2 & 3: create two nodes ---
type "Hello"
send Enter
wait_for "○ Hello"

type "World"
wait_for "○ World"

# Sanity: both nodes visible while the editor is live.
assert_contains "○ Hello"
assert_contains "○ World"

# Arm remain-on-exit so the tmux pane survives after lflow exits.
# This must happen before C-c so the session is not reaped on process exit.
tmux set-option -t "${SESSION}" remain-on-exit on

# --- step 4: quit via C-c ---
send C-c

# The editor renders its final frame (the styled outline) and exits.
# With remain-on-exit the pane stays alive. Poll the full scrollback until
# the outline appears (up to 5 s).
wait_for_scrollback "○ World" 5

# --- step 5: core regression assertions ---
# Both bullet lines must be present in the scrollback after the editor has
# exited. Before the fix these lines were absent (bubbletea rendered an
# empty string as the final frame, which wiped the screen on exit).
scrollback_contains "○ Hello"
scrollback_contains "○ World"

assert_no_crash
pass
