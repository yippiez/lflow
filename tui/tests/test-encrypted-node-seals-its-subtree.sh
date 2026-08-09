#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# An Encrypted node is a vault: sealing it takes its whole subtree off the
# outline and leaves one row of garble behind, and alt+e will not show any of it
# again without the password. This is the gesture the whole feature is: the row
# has to LOOK like nothing, and entering it has to ask.

setup; use_persist_db; launch

type "bank"
send Enter
send Tab
type "iban TR00"
send Up

# make it a vault
type "/"
type "type"
send Enter
type "encrypted"
send Enter

wait_for "seal this node" 8
type "hunter2"
send Tab
type "hunter2"
send Enter

# freshly sealed vaults stay open — the password was just proven
wait_for "unlocked" 20
assert_contains "bank"
assert_contains "iban TR00"

# alt+e seals it: the title and the subtree both go
send M-e
wait_for "sealed" 10
assert_not_contains "bank"
assert_not_contains "iban TR00"

# alt+e again asks, and refuses the wrong answer without saying which part
send M-e
wait_for "unlock ·" 8
type "hunter3"
send Enter
wait_for "Wrong key" 20
assert_not_contains "iban TR00"

type "hunter2"
send Enter
wait_for "iban TR00" 20
assert_contains "bank"

# and it survives a relaunch sealed: the blob is the only copy on disk, so the
# reopened outline shows noise again and asks again
send M-e
wait_for "sealed" 10
send C-s
sleep 1
reopen
assert_not_contains "bank"
assert_not_contains "iban TR00"
assert_contains "sealed"

send M-e
wait_for "unlock ·" 8
type "hunter2"
send Enter
wait_for "iban TR00" 20

assert_no_crash
pass
