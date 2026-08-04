#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Duplicating A..C as a row selection must produce A B C A B C. Inserting each
# copy beside its source produced the interleaved A A B B C C order.

setup; launch

type "alpha-one"
send Enter
type "beta-two"
send Enter
type "gamma-three"
wait_for "gamma-three"

send Up Up
send S-Down S-Down
type "/"
type "duplicate"
send Enter
wait_for "duplicated 3 nodes"

pane="$(snapshot)"
order="$(printf '%s\n' "${pane}" | grep -E 'alpha-one|beta-two|gamma-three' | sed -E 's/^.*(alpha-one|beta-two|gamma-three).*$/\1/' | paste -sd ' ' -)"
if [[ "${order}" != "alpha-one beta-two gamma-three alpha-one beta-two gamma-three" ]]; then
    fail "duplicates did not land as one block; order=${order}"
fi
assert_no_crash
pass
