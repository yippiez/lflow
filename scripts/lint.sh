#!/usr/bin/env bash
#
# lint.sh — ast-grep design-invariant lint harness for lflow.
#
# Runs the rule self-tests first (so a broken rule fails loudly), then scans the
# repository for any violations of the project's design invariants. Exits
# non-zero on the first failure.
#
# Rules live in rules/, their tests in rule-tests/, wired up by sgconfig.yml.

set -euo pipefail

# Resolve repo root from this script's location so it works from any cwd.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT}"

# Pick the ast-grep binary (shipped as both `ast-grep` and `sg`).
if command -v ast-grep >/dev/null 2>&1; then
  SG=ast-grep
elif command -v sg >/dev/null 2>&1; then
  SG=sg
else
  printf '\033[31m✗\033[0m ast-grep not found on PATH (install from https://github.com/ast-grep/ast-grep)\n' >&2
  exit 127
fi

green() { printf '\033[32m%s\033[0m' "$1"; }
red() { printf '\033[31m%s\033[0m' "$1"; }

fail() {
  printf '%s %s\n' "$(red ✗)" "$1" >&2
  exit 1
}

# 1. Rule self-tests: every rule must have valid + invalid snippets that behave.
#    --skip-snapshot-tests keeps this to behavioural (valid passes / invalid
#    matches) checks without committing label-position snapshot files.
printf 'lint: running rule self-tests …\n'
if ! "${SG}" test --skip-snapshot-tests; then
  fail "rule self-tests failed"
fi
printf '%s rule self-tests passed\n' "$(green ✓)"

# 2. Scan the repo. ast-grep scan exits non-zero when error-severity rules match;
#    we additionally fail on ANY reported finding (including warnings) so the
#    harness gates regressions of every invariant, not just the error-level ones.
printf 'lint: scanning repository …\n'
findings="$("${SG}" scan --report-style=rich 2>&1 || true)"
if [ -n "${findings}" ]; then
  printf '%s\n' "${findings}"
  fail "ast-grep scan reported findings"
fi
printf '%s no findings\n' "$(green ✓)"

printf '%s lint clean\n' "$(green ✓)"
