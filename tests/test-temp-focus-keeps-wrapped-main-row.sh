#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Moving Down from a wrapped last main node into the Temporary Domain turns the
# main outline into a read-only region. That region must keep every visual line
# of the stashed cursor row visible, rather than pinning only its first line at
# the bottom of the main window.

WIN_W=50
WIN_H=14
setup; launch

for i in 1 2 3 4 5 6 7 8; do
    type "short ${i}"
    send Enter
done
type "wrapped main node keeps the distinctive final words visible"
wait_for "words visible"

# At the text end, Down crosses directly into temp. The trailing wrapped line of
# the main node must remain on screen above the status bar.
send Down
wait_for "◌"
assert_contains "words visible"
assert_no_crash
pass
