#!/usr/bin/env bash
#
# scripts/check.sh — the full static quality gate: formatting, vet, ast-grep
# rules (+ their rule-tests), stale-reference guards, unit tests and the
# architecture tests (import layering, node-type registry parity). The e2e
# suite lives separately in scripts/test.sh.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"
fail=0

echo "→ gofmt"
unformatted="$(gofmt -l tui mobile)"
if [[ -n "${unformatted}" ]]; then
    echo "gofmt needed:"; echo "${unformatted}"; fail=1
fi

echo "→ go vet"
go vet --tags fts5 ./... || fail=1

echo "→ ast-grep rules"
ast-grep test || fail=1
ast-grep scan || fail=1

echo "→ stale-path guard (no packages/ imports outside legacy)"
if grep -rn 'github.com/lflow/lflow/packages|packages/' --include='*.go' tui mobile; then
    echo "stale packages/ references above"; fail=1
fi

echo "→ package-doc guard (doc comment names a different package)"
while IFS= read -r f; do
    doc="$(grep -m1 -oP '^// Package \K[a-z0-9_]+' "$f" || true)"
    pkg="$(grep -m1 -oP '^package \K[a-z0-9_]+' "$f" || true)"
    if [[ -n "${doc}" && -n "${pkg}" && "${doc}" != "${pkg}" ]]; then
        echo "$f: doc says 'Package ${doc}' but package is '${pkg}'"; fail=1
    fi
done < <(find tui mobile -name '*.go' ! -name '*_test.go')

echo "→ unit tests"
go test --tags fts5 ./... || fail=1


if (( fail )); then
    echo "CHECK FAILED"
    exit 1
fi
echo "CHECK OK"
