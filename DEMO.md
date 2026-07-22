# DEMO.md — recording a demo video of an lflow change

A visible change ships with a short video. There is no committed demo tooling:
you assemble the recipe below in a scratch directory, record, render, and throw
the scratch away. This file is the spec — follow it and every clip looks the
same.

Deps: `tmux`, `ffmpeg`, `python3` with PIL, and `scripts/ansishot.py` (already in
the repo — it paints a `tmux capture-pane -e` dump as a PNG).

Output: an mp4, ~30-60 s, ~900-1300 px wide, a caption bar burned along the top.

## 1. A throwaway sandbox, always

The demo never touches the real outline at `~/.local/share/lflow/lflow.db`.
Every lflow call goes through a wrapper that `env -i`s a fresh HOME/XDG and
refuses anything outside the scratch root:

```bash
# /tmp/lflow-demo/sandbox — chmod +x
set -euo pipefail
root=/tmp/lflow-demo; home=$root/env-$1; shift
case "$home" in "$root"/env-*) ;; *) echo "refusing: $home" >&2; exit 9;; esac
exec env -i PATH=/usr/local/bin:/usr/bin:/bin TERM="${TERM:-xterm-256color}" \
  HOME="$home" XDG_CONFIG_HOME="$home/.config" \
  XDG_DATA_HOME="$home/.local/share" XDG_CACHE_HOME="$home/.cache" \
  ${LFLOW_NO_DAEMON:+LFLOW_NO_DAEMON=$LFLOW_NO_DAEMON} ${TMPDIR:+TMPDIR=$TMPDIR} \
  "$root/bin/lflow" "$@"
```

> **Never** write `export HOME=/tmp/… XDG_DATA_HOME=$HOME/…` in one statement.
> Bash expands `$HOME` to the OLD value, and the demo seeds nodes into the live
> outline. That has happened; the wrapper exists so it cannot happen again.

Build the binary under test into the scratch root, always with fts5:

```bash
go build --tags fts5 -o /tmp/lflow-demo/bin/lflow ./pkg/tui
```

Seed the outline with `sandbox <demo> node add …`. New children land on top
(priority up), so add them in reverse to get the display order you want, and
read ids back with `node list <id> --format json`. For state the CLI cannot
express — an image node's PNG blob, an editor preference — write a throwaway
`-tags fts5` Go program against `database.PutBlob` / `database.SetSetting` and
run it with `LFLOW_NO_DAEMON=1` (the sqlite3 CLI cannot open these DBs; it
lacks fts5). Kill the sandbox daemon first so the file is free.

## 2. Record

The editor runs in a detached tmux pane; a background loop snapshots the pane on
a timer while a driver script sends keys. Two files come out of it: an index of
`frame → timestamp`, and captions of `timestamp → text`.

```bash
tmux new-session -d -s demo -x 100 -y 30 /tmp/lflow-demo/sandbox mydemo node open Root
tmux set-option -t demo status off

# capture loop, in the background for the whole take
( i=0; while :; do i=$((i+1)); f=$(printf "%05d" $i)
    if tmux capture-pane -p -e -t demo > frames/$f.ans 2>/dev/null; then
      printf "%s\t%s\n" "$f" "$(date +%s.%N)" >> index.tsv
    else rm -f frames/$f.ans; fi          # between scenes: no session, no frame
    sleep 0.15
  done ) &

say() { printf "%s\t%s\n" "$(date +%s.%N)" "$1" >> captions.tsv; }
k()   { tmux send-keys -t demo "$@"; }     # key names: M-s, C-q, Enter, Down
t()   { tmux send-keys -t demo -l "$1"; }  # a literal string
type_slow() { local s=$1 i; for ((i=0;i<${#s};i++)); do
    tmux send-keys -t demo -l "${s:$i:1}"; sleep 0.05; done; }
```

Then the take itself is just `say` / `k` / `sleep`:

```bash
say "alt+s  ·  flash labels every visible row with its actions"
k M-s; sleep 2.8
say "every row now offers 'zoom' — that is the new verb"
sleep 2.5
```

Two things worth knowing:

- **Keep a scene shorter than the pane.** The editor draws inline in scrollback,
  so a frame that outgrows `-y` scrolls and leaves stale rows above — the capture
  then shows a dead status bar mid-screen. Split the demo into scenes: quit
  (`C-q`), start a second tmux session (small, fresh) and carry on. The capture
  loop survives the gap because it drops frames it cannot take.
- **Read dynamic labels back, don't hardcode them.** The flash menu assigns
  letters by layout, so grep the pane for the row and verb:
  `tmux capture-pane -p -t demo | grep -F "flash menu" | grep -o "[a-z0-9] zoom" | cut -c1`.

## 3. Render

A Python step turns the take into the mp4. The exact numbers matter — they are
the house look:

1. **Segment.** Walk `index.tsv`; a segment's key is `(sha1 of the .ans, caption
   in force at that timestamp)`. Consecutive frames with the same key collapse
   into one segment whose duration is the sum of the gaps, clamped to
   `[0.15 s, 3 s]`. A 60 s take is usually 15-40 segments.
2. **Paint** each unique frame once: `python3 scripts/ansishot.py out.png < frame.ans`
   (DejaVu Sans Mono, 22 px, 13×28 cells, 16 px pad, `#181818` ground).
3. **Caption bar.** Canvas width = widest frame, minimum **900 px**; the bar is
   `#0b0b0b` with a `#3c3c3c` hairline under it, text DejaVu Sans Mono **Bold
   21 px** in **`#e5c07b`**, 16 px from the left, 14 px pad, 28 px per wrapped
   line; the bar grows to fit the longest caption of the take. Frame body pastes
   below it on a `#181818` canvas.
4. **Mux** with a concat demuxer — `file 'x.png'` / `duration 1.234` per segment,
   last file repeated — then:

```bash
ffmpeg -f concat -safe 0 -i concat.txt -vsync vfr -pix_fmt yuv420p \
  -c:v libx264 -crf 20 -movflags +faststart \
  -vf "fps=12,scale=trunc(iw/2)*2:trunc(ih/2)*2" out.mp4
```

Rendering is cheap (a couple of seconds) because only unique frames are painted;
iterate on captions freely.

### Stills, for what a terminal cannot show

Some things are invisible to `capture-pane` — a kitty/sixel graphics payload,
for instance, which tmux neither renders nor echoes. Append still frames after
the recording (same canvas, same caption bar, image centred), and **say in the
caption that it is decoded, not captured**. To get one honestly: point the
editor's `TMPDIR` at a scratch dir, trigger the action, copy the temp payload
out while the child still waits, and decode it (`convert sixel:seq.sixel
out.png`). Never fake the frame.

## 4. House style

- **Captions carry the narration.** One lowercase line, no emoji, `·` as the
  separator, and name the key: `"alt+r on the quadratic — export LaTeX"`.
- **Slow enough to read.** 2.5-3.5 s per caption; `type_slow` for anything the
  viewer should watch land.
- **Show the payoff, then name it.** Press the key, hold on the result, then a
  caption saying what just happened ("a mirror holds no children of its own —
  zoom resolved to the source").
- **Open by naming the thing**, close on a settled frame.
- Demo the edge case, not just the happy path — the mirror row, the sub-node,
  the empty container.
- Seed content that reads instantly: real formulas, real project names.

## 5. Checklist

- [ ] built with `--tags fts5`, into the scratch root
- [ ] every lflow call went through `sandbox`; real outline untouched
- [ ] no scene outgrew its pane (no stale status bar mid-frame)
- [ ] captions lowercase, keys named, ≥2.5 s each
- [ ] stills labelled as decoded
- [ ] 30-60 s, plays on a phone
- [ ] scratch root deleted; sandbox daemons killed (`pgrep -f "lflow-demo.*serve"`)
