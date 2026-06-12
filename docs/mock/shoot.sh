#!/usr/bin/env bash
# shoot.sh — render each mock variant in a tmux pane and capture to PNG.
# Pipeline (per pi-prompt-chain's tmux-shot.sh): tmux capture-pane -e ->
# a2p.py (ANSI -> Pango markup) -> pango-view PNG on black background.
set -euo pipefail
cd "$(dirname "$0")"
OUT_DIR="/tmp/lflow-design/images"
mkdir -p "$OUT_DIR"
COLS=74
FONT="DejaVu Sans Mono"; PT=15; MARGIN=14; DARK="#000000"

# variant:rows (rows = exact line count; mock.py emits no trailing newline)
VARIANTS=(
  final:9 slash-menu:16 slash-finder:9
  cmd-find-best:3 cmd-find-error:4 cmd-list:9 cmd-append:5
  cmd-wf:8 cmd-sync-local:5 summary:3
)

for VR in "${VARIANTS[@]}"; do
  V="${VR%%:*}"; R="${VR##*:}"
  tmux kill-session -t lfmock 2>/dev/null || true
  tmux new-session -d -s lfmock -x "$COLS" -y "$R" "python3 mock.py $V; sleep 60"
  sleep 0.4
  tmux capture-pane -t lfmock -e -p \
    | python3 a2p.py --rows "$R" --cols "$COLS" > "/tmp/lfmock-$V.pango"
  pango-view --no-display -q --markup --font "$FONT $PT" \
    --background "$DARK" --margin "$MARGIN" -o "$OUT_DIR/$V.png" "/tmp/lfmock-$V.pango"
  tmux kill-session -t lfmock 2>/dev/null || true
  echo "$OUT_DIR/$V.png"
done

# prune captures no longer referenced by the report
rm -f "$OUT_DIR"/{panel-gray,panel-black,conn-tree,conn-indent,glyph-circle,glyph-dot,cursor-row,cursor-bar,bottom-info,bottom-keys,bottom-min,bars-full,bars-min,picker,cmd-ambiguous,cmd-sync,cmd-sync-dry,wf-timer,slash-mirror}.png
