# shellcheck shell=bash
#
# lib.sh — bash/tmux end-to-end harness for the lflow inline editor.
#
# Isolation model: each test runs the real lflow binary in its OWN tmux session,
# against a throwaway :memory: SQLite DB under an isolated HOME. Because :memory:
# is per-process, the starting tree is built by TYPING in the editor, never by
# seeding from a separate process. For the rare reopen/persistence test, call
# 'use_persist_db' BEFORE launch to switch to a temp FILE db so a relaunch keeps
# data.
#
# Sourced by every test-*.sh. The sourcing script is expected to have run
# `set -euo pipefail` already, but we set it here too for safety.

set -euo pipefail

# ---------------------------------------------------------------------------
# Globals (populated by setup/launch).
# ---------------------------------------------------------------------------
TEST_NAME="$(basename "${BASH_SOURCE[1]:-${0}}")"
SESSION=""
TEST_HOME=""
WIN_W="${WIN_W:-80}"
WIN_H="${WIN_H:-24}"
LAST_PANE=""

# settleAfterLaunch: the single fixed pause we allow — the inline bubbletea
# editor needs a moment to paint its first frame after the pane spawns. Every
# other synchronization uses wait_for against the rendered pane.
SETTLE_AFTER_LAUNCH=1.2

# ---------------------------------------------------------------------------
# Binary resolution: honor $LFLOW_BIN if set and executable; else build once to
# /tmp/lflow-e2e-bin/lflow (build only if that path is missing).
# ---------------------------------------------------------------------------
_resolve_bin() {
    if [[ -n "${LFLOW_BIN:-}" && -x "${LFLOW_BIN}" ]]; then
        BIN="${LFLOW_BIN}"
        return
    fi
    BIN="/tmp/lflow-e2e-bin/lflow"
    if [[ ! -x "${BIN}" ]]; then
        local root
        root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
        mkdir -p /tmp/lflow-e2e-bin
        ( cd "${root}" && go build --tags fts5 -o "${BIN}" ./pkg/tui )
    fi
}

# ---------------------------------------------------------------------------
# setup: isolated HOME with a :memory: settings.json + a unique session name.
# ---------------------------------------------------------------------------
setup() {
    _resolve_bin

    TEST_HOME="$(mktemp -d "/tmp/lflow-e2e-home.XXXXXX")"
    mkdir -p "${TEST_HOME}/.lflow"
    printf '%s\n' '{"dbPath":":memory:"}' > "${TEST_HOME}/.lflow/settings.json"

    local slug
    slug="$(printf '%s' "${TEST_NAME}" | tr -cd 'A-Za-z0-9_')"
    SESSION="lflow_e2e_${slug}_$$_${RANDOM}"
}

# ---------------------------------------------------------------------------
# use_persist_db: (optional, call BEFORE launch) switch settings.json dbPath to
# a temp FILE so a reopen persists data across relaunches.
# ---------------------------------------------------------------------------
use_persist_db() {
    local dbfile="${TEST_HOME}/.lflow/persist.db"
    printf '{"dbPath":"%s"}\n' "${dbfile}" > "${TEST_HOME}/.lflow/settings.json"
}

# ---------------------------------------------------------------------------
# launch: start a detached tmux session running `<bin> node open` (no node arg —
# opens an empty in-memory outline ready to type). Then settle for the paint.
# ---------------------------------------------------------------------------
launch() {
    tmux new-session -d -s "${SESSION}" -x "${WIN_W}" -y "${WIN_H}" \
        "HOME='${TEST_HOME}' XDG_CONFIG_HOME='${TEST_HOME}' XDG_DATA_HOME='${TEST_HOME}' XDG_CACHE_HOME='${TEST_HOME}' TERM=xterm-256color '${BIN}' node open"
    sleep "${SETTLE_AFTER_LAUNCH}"
}

# ---------------------------------------------------------------------------
# reopen: kill + relaunch the same session/HOME (requires use_persist_db).
# ---------------------------------------------------------------------------
reopen() {
    tmux kill-session -t "${SESSION}" 2>/dev/null || true
    # a fresh name avoids any lingering-server race on the old name
    SESSION="${SESSION}_r"
    launch
}

# ---------------------------------------------------------------------------
# send <keys...>: dispatch named tmux keys (Enter, Tab, BSpace, Down, Up, Left,
# Right, M-r, M-e, C-s, Escape, ...). Small 40ms gap after each.
# ---------------------------------------------------------------------------
send() {
    tmux send-keys -t "${SESSION}" "$@"
    sleep 0.04
}

# ---------------------------------------------------------------------------
# type "<text>": send a literal string (no key interpretation). 60ms gap.
# ---------------------------------------------------------------------------
type() {
    tmux send-keys -t "${SESSION}" -l "$1"
    sleep 0.06
}

# ---------------------------------------------------------------------------
# snapshot: echo the pane plain-text (no SGR escapes).
# ---------------------------------------------------------------------------
snapshot() {
    LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
    printf '%s\n' "${LAST_PANE}"
}

# ---------------------------------------------------------------------------
# wait_for "<sub>" [timeout_s]: poll the pane (80ms) until it contains <sub>,
# else fail with the last pane. Default 5s.
# ---------------------------------------------------------------------------
wait_for() {
    local sub="$1"
    local timeout="${2:-5}"
    local deadline
    deadline=$(( $(date +%s%N) + timeout * 1000000000 ))
    while :; do
        LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
        if [[ "${LAST_PANE}" == *"${sub}"* ]]; then
            return 0
        fi
        if (( $(date +%s%N) > deadline )); then
            fail "timed out waiting for: ${sub}"
        fi
        sleep 0.08
    done
}

# ---------------------------------------------------------------------------
# assert_contains "<sub>": refresh the snapshot and fail unless it contains <sub>.
# ---------------------------------------------------------------------------
assert_contains() {
    LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
    if [[ "${LAST_PANE}" != *"$1"* ]]; then
        fail "expected pane to contain: $1"
    fi
}

# ---------------------------------------------------------------------------
# assert_not_contains "<sub>": fail if the snapshot DOES contain <sub>.
# ---------------------------------------------------------------------------
assert_not_contains() {
    LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
    if [[ "${LAST_PANE}" == *"$1"* ]]; then
        fail "expected pane to NOT contain: $1"
    fi
}

# ---------------------------------------------------------------------------
# assert_no_crash: fail if the pane shows a Go panic / runtime error / stack.
# ---------------------------------------------------------------------------
assert_no_crash() {
    LAST_PANE="$(tmux capture-pane -t "${SESSION}" -p)"
    if [[ "${LAST_PANE}" == *"panic"* || "${LAST_PANE}" == *"runtime error"* || "${LAST_PANE}" == *"goroutine "* ]]; then
        fail "crash detected in pane"
    fi
}

# ---------------------------------------------------------------------------
# pass / fail.
# ---------------------------------------------------------------------------
pass() {
    printf 'PASS %s\n' "${TEST_NAME}"
    exit 0
}

fail() {
    printf 'FAIL %s: %s\n' "${TEST_NAME}" "$1" >&2
    printf -- '--- last pane ---\n%s\n--- end pane ---\n' "${LAST_PANE}" >&2
    exit 1
}

# ---------------------------------------------------------------------------
# EXIT trap: always kill the tmux session and remove the temp HOME.
# ---------------------------------------------------------------------------
_cleanup() {
    if [[ -n "${SESSION}" ]]; then
        tmux kill-session -t "${SESSION}" 2>/dev/null || true
    fi
    if [[ -n "${TEST_HOME}" && -d "${TEST_HOME}" ]]; then
        rm -rf "${TEST_HOME}"
    fi
}
trap _cleanup EXIT
