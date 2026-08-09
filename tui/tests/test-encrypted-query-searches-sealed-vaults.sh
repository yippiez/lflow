#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# The reason the Encrypted Query type exists. An ordinary query scans the nodes
# table, and a sealed vault's contents were never in it — so the plain query
# reports nothing and is right to. The Encrypted Query asks for a key, opens
# what that key opens, and finds the same text inside the ciphertext.

setup; launch

type "vault"
send Enter
send Tab
type "iban zebra quokka"
send Enter
send BTab

# a plain query for a word that lives ONLY inside the vault
type "zebra"
type "/"
type "type"
send Enter
type "query"
send Enter
send Enter

# …and an encrypted query for the other word that lives only inside it
type "quokka"
type "/"
type "type"
send Enter
type "encquery"
send Enter

# seal the vault: back up to its row (encrypted query, plain query, the child)
send Up
send Up
send Up
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
wait_for "unlocked" 20
send M-e
wait_for "sealed" 10
assert_not_contains "iban zebra quokka"

# the plain query finds nothing — there is nothing in the table to find
send End
send Down
send M-r
wait_for "0 hits" 10
assert_not_contains "iban zebra quokka"

# the encrypted query asks for a key, and finds it
send End
send Down
send M-r
wait_for "search your vaults" 8
type "hunter2"
send Enter
wait_for "iban zebra quokka" 20
assert_contains "1 hit"

assert_no_crash
pass
