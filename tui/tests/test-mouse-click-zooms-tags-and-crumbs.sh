#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# The three mouse gestures, end to end against a real terminal's mouse reports:
#
#   the outline circle    click → zoom into that node (alt+right, with a mouse)
#   a #tag                click → /goto, already searching that tag
#   a breadcrumb segment  hover → it lights up; click → the view walks there
#
# The point of driving them through tmux is the COORDINATES: the editor records
# the cells it painted and a click is a lookup into that record, so a real
# report from a real terminal is the only thing that proves the recorded columns
# are the ones on screen.

setup; launch

type "lflow"
send Enter; send Tab
type "editor"
send Enter; send Tab
type "mouse.go "; type "#"; type "mouse"; send Space
send Enter
type "view.go "; type "#"; type "mouse"; send Space
wait_for "○ view.go #mouse"
assert_contains "○ lflow"

# ── the circle: click it, zoom into that node ───────────────────────────────
read -r gx gy <<< "$(cell_of "○ editor")"
mouse click "${gx}" "${gy}"
wait_for "› editor ·"
assert_not_contains "○ lflow"     # the zoomed view holds only editor's children
assert_contains "○ mouse.go #mouse"

# ── the breadcrumb: hover lights the segment, a click walks the view there ──
read -r _ by <<< "$(cell_of "› editor ·")"
mouse move 1 "${by}"
# the hovered crumb is underlined (SGR 4) — the plain-text pane cannot show that,
# so read the styled capture
styled="$(tmux capture-pane -t "${SESSION}" -p -e | sed -n "$(( by + 1 ))p")"
if [[ "${styled}" != *$'\033[4m'* ]]; then
    LAST_PANE="${styled}"
    fail "hovering the first crumb did not light it up"
fi

mouse click 1 "${by}"
wait_for "○ lflow"                # back out to the root the crumb named
assert_contains "○ editor"

# ── a tag: click it, /goto opens searching that tag ─────────────────────────
# the finder searches the DB, so the typed nodes have to be in it first
send C-s
wait_gone "unsaved"
read -r tx ty <<< "$(cell_of "#mouse")"
mouse click "$(( tx + 2 ))" "${ty}"
wait_for "/goto #mouse"
# the OTHER #mouse node is the hit; the node clicked from is never its own target
assert_contains "view.go #mouse"

send Escape
wait_for "○ lflow"

assert_no_crash
pass
