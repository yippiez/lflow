#!/usr/bin/env bash
#
# scripts/test.sh — build the lflow binary once, then run the bash/tmux e2e
# regression suite (every pkg/e2e/test-*.sh). Prints PASS/FAIL per script and a
# final summary; exits non-zero if any test failed.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

TMPDIR_BIN="$(mktemp -d "/tmp/lflow-e2e-suite.XXXXXX")"
trap 'rm -rf "${TMPDIR_BIN}"' EXIT

BIN="${TMPDIR_BIN}/lflow"
echo "building lflow binary..."
go build --tags fts5 -o "${BIN}" ./pkg/tui
export LFLOW_BIN="${BIN}"

passed=0
failed=0
failures=()

shopt -s nullglob
tests=( "${ROOT}"/pkg/e2e/test-*.sh )
shopt -u nullglob

if (( ${#tests[@]} == 0 )); then
    echo "no tests found (pkg/e2e/test-*.sh)"
    exit 0
fi

for t in "${tests[@]}"; do
    name="$(basename "${t}")"
    if bash "${t}"; then
        passed=$(( passed + 1 ))
    else
        failed=$(( failed + 1 ))
        failures+=( "${name}" )
        echo "FAIL ${name}"
    fi
done

echo "------------------------------------------------------------"
echo "${passed} passed, ${failed} failed"
if (( failed > 0 )); then
    printf 'failed: %s\n' "${failures[*]}"
    exit 1
fi
