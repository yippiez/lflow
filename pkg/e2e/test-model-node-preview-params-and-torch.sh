#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"

# Contract: the Model node is a neural network composed AS an outline — a row is
# a building block with its arguments, or a container whose parts are its
# children.
#
#   sequential          /type Model
#   ├─ Linear 784 256   a building block; Enter kept the Model type going
#   ╰─ ReLU
#
# Expected:
#   - Enter from a Model row opens another Model row (the stack continues), so
#     the children light up as blocks without being converted one by one.
#   - a block row reads back its own parameter count (201K), and the container
#     reads back the flattened subtree plus the total.
#   - alt+e opens the PyTorch export in the shared code block (line-number
#     gutter, the imports, one constructor per row).
#   - alt+r writes that same export into the ephemeral run band.

setup
launch

# --- a container row, converted once ---------------------------------------

type "sequential"
wait_for "○ sequential"
send End
# "/" must land alone: a burst of runes arrives as ONE key message, and only a
# lone "/" opens the slash menu.
type "/"
wait_for "/duplicate"
type "type"
wait_for "Set this node's type"
send Enter
wait_for "type:"
type "ml"
send Enter
wait_for "○ sequential"

# --- Enter keeps the stack going: the children are Model rows too ----------

send End
send Enter
send Tab                    # the fresh sibling becomes the container's child
type "Linear 784 256"
# 784·256 + 256 = 200,960 parameters — only a Model row prints that tail
wait_for "201K"
assert_contains "╰─"

send Enter                  # another Model row, no /type needed
type "ReLU"
wait_for "○ ReLU"

# --- the container reads back its subtree ----------------------------------

assert_contains "Linear(784→256) → ReLU"

# --- alt+e: the PyTorch export in the shared code block --------------------

send Up                     # onto the Linear row
wait_for "○ Linear 784 256"
send M-e
wait_for "import torch"
assert_contains "import torch.nn as nn"
assert_contains "nn.Linear(784, 256)"
assert_contains "1 │"       # the code block's line-number gutter

send Escape
wait_for "○ Linear 784 256"

# --- alt+r: the same export in the run band --------------------------------

send M-r
wait_for "PyTorch → output"
assert_contains "nn.Linear(784, 256)"

assert_no_crash
pass
