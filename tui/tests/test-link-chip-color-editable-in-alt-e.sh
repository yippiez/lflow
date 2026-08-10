#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Contract: a link chip's COLOR is editable where its name and target already
# are — the ⌥e page — and it is the chip's own, not a global preference.
#
#   ⌥e on "→example.com"    the page: name, target, color
#   tab tab                 walks name → target → color
#   ←/→ on the color row    cycles swatches ("default" first, then the palette)
#   enter                   saves all three at once
#
# Expected:
#   - the ⌥e page lists a color row beside name and target, and says so in its
#     hint line;
#   - ←/→ move the swatch cursor (● ) along the row, and typing there edits
#     nothing (the row is swatches, not text);
#   - the picked color survives a reopen: it is stored per chip id, not held in
#     the session.

setup
use_persist_db
launch

# --- a link chip: "[[" then a URL typed straight into the query -------------

# each "[" must land alone: a burst of runes arrives as ONE key message, and the
# finder opens on the second bracket keystroke
type "["
type "["
wait_for "Link to node"
type "https://example.com"
send Enter
wait_for "→example.com"

# the caret is parked just after the chip, which is where ⌥e finds it
send M-e
wait_for "edit link"

# --- the page has all three fields -----------------------------------------

assert_contains "name"
assert_contains "target"
assert_contains "color"
assert_contains "tab switch field"
# the preview shows the chip itself, in the color it will wear
assert_contains "→example.com"

# --- tab walks to the color row, ←→ cycles it -------------------------------

send Tab            # name → target
send Tab            # target → color
send Right          # "default" → the first palette color

# the swatch cursor moved off the default swatch: the row now reads "· ● · …"
assert_contains "· ●"

# a rune on the color row is not text — it must not land in the name
type "z"
assert_not_contains "namez"

send Enter
wait_for "→example.com"
# the name and target came through the color edit untouched
assert_not_contains "edit link"
assert_not_contains "→examplez.com"

# --- the color is stored per chip and survives a reopen ---------------------

send C-s
sleep 0.3
reopen
wait_for "→example.com"

send M-e
wait_for "edit link"
send Tab
send Tab
# reopened on the SAME swatch it was saved with — the second one, not "default"
assert_contains "· ●"

assert_no_crash
pass
