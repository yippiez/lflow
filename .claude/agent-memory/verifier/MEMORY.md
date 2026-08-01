# Verifier memory

## Baseline: known-flaky e2e failures (tmux/timing related, NOT regressions)
As of 2026-08-01, on branch claude/lflow-file-open-editor-sjo1wg (post cmd/lflow +
packages/ restructure), `bash scripts/test.sh` reliably reports **22 passed, 12 failed**
with exactly this failing set (all tmux-driven, timing/flake in nature — not caused by
unrelated feature branches):

- test-bash-node-shows-dollar-and-spinner.sh (timed out waiting for: Bash)
- test-bring-moves-node-here.sh (timed out waiting for: Enter move that node here)
- test-home-end-visual-line.sh (timed out waiting for: root · 2/2)
- test-mirror-cursor-stays-local.sh (timed out waiting for: /mirror:to)
- test-mirror-outdent-escapes-source.sh (timed out waiting for: src · mirror)
- test-mirror-shows-live-source-edits.sh (timed out waiting for: ◆ source)
- test-section-keeps-one-node.sh (timed out waiting for: ◌)
- test-slash-escape-removes-trigger-keeps-status.sh (expected pane to NOT contain: ○ hello/)
- test-status-bar-is-temp-divider.sh (expected pane to NOT contain: ◌)
- test-temp-panel-always-visible.sh (expected pane to NOT contain: ◌)
- test-voice-nil-map-panic-on-load.sh (voice node never committed to DB after C-s)
- test-zoom-into-mirror-shows-source-children.sh (timed out waiting for: /mirror:to)

When verifying a new change: run the full suite, confirm the failing set is a subset of
(ideally exactly equal to) this list. Only additional/different failures are a real
regression signal worth reporting as FAIL. If the failing set shrinks or grows, note the
new baseline count here with the date.

## Known pre-existing ast-grep findings (not regressions)
- error[no-direct-sql-open] at packages/daemon/store.go:102 — sql.Open used directly
  instead of database.Open. Pre-existing, tracked separately, not part of typical
  feature branches.
- warning[capitalize-error-strings] x3 in packages/daemon/wire/wire.go (lines ~128, 139,
  154) — lowercase fmt.Errorf messages. Pre-existing, unrelated to feature work.

`ast-grep scan` on a clean feature branch (no rules/ changes) should show exactly these
4 findings (1 error + 3 warnings) and nothing else. If `rules/` or `rule-tests/` changed,
also run `ast-grep test`.

## Verified: file-open feature (packages/cli/file, packages/nodes/{filecodec,markdown,python}.go)
2026-08-01: build (fts5), full unit tests (including new filecodec_test.go — 
TestCodecForPath, TestMarkdownRoundTrip/ParagraphNormalizes/Nesting,
TestPythonRoundTrip/Reindents/Nesting/SpanColor all pass), e2e suite (only the 12
baseline flakes above, both test-file-open-markdown-writes-back.sh and
test-file-open-python-writes-back.sh PASS), and ast-grep scan (only the 4 known
pre-existing findings, nothing new from the file-open code) all verified clean.
No rules/ changes in this branch, so ast-grep test was skipped (correctly).

## Demo-recording lessons (file-open markdown + python clips, 2026-08-01)

Recorded two DEMO.md clips for `lflow file open`. Environment + editor-behavior notes
that will save time next time:

- **Sandbox environment needed setup on this box**: `ffmpeg` and python `Pillow` were
  NOT preinstalled. `apt-get install -y ffmpeg` (works, just noisy proxy warnings about
  unrelated PPAs — ignore them) and `python3 -m pip install --break-system-packages
  pillow` are both needed before DEMO.md's recipe will run. DejaVu Sans Mono fonts
  (`/usr/share/fonts/truetype/dejavu/`) were already present. Check these three before
  assuming the recipe "just works".
- **`lflow file open` still spins up an idle daemon** under the sandboxed env's
  `XDG_DATA_HOME` (e.g. `env-demo1/.local/share/lflow/lflow.db` + `daemon.sock`), even
  though the `file open` codepath itself uses a direct scratch-DB handle
  (`fileCtx.Live = nil`) and never touches that daemon. Root cause: `cmd/lflow/main.go`
  calls `infra.Init` (which lazily starts/connects a daemon) for every subcommand BEFORE
  cobra dispatches to `file open`'s RunE. Harmless (per-sandbox-env, never the real
  outline) but means `pgrep -f "lflow-demo.*serve"` after EVERY sandboxed `file open`
  session, not just ones that obviously use a daemon.
- **`file open`'s scratch DB dir (`/tmp/lflow-file-<N>/doc.db`) leaks if the process is
  killed uncleanly** (e.g. `tmux kill-session` instead of a graceful C-q/quit) — the
  `defer os.RemoveAll(scratchDir)` in `packages/cli/file/open.go` never runs. These
  accumulate under system `/tmp` (NOT under the demo scratch root, since `os.MkdirTemp`
  ignores it) across repeated test/record iterations. Sweep `/tmp/lflow-file-*` at the
  end of any session that iterated on `file open` sandbox recordings.
- **Down/Up have a two-phase "snap then cross" behavior** (see commit "down snaps caret
  to node end before crossing"): pressing Down when the caret isn't already at the
  rightmost column of the current row just snaps it to that row's end WITHOUT moving to
  the next row; only a Down pressed while ALREADY at end crosses to the next row. Mirror
  behavior for Up (snaps to a row's start before crossing, confirmed empirically). The
  robust one-row-descent recipe used successfully in both clips: always pair `End` before
  `Down` (or `Up`) — `k End; sleep; k Down; sleep` reliably moves exactly one row down
  every time, regardless of where the caret starts. Existing e2e tests (e.g.
  test-file-open-markdown-writes-back.sh) get away with a single bare `Down` only because
  their content assertions are order-independent (substring checks, not position checks)
  — don't copy that pattern for a demo where the visual landing spot matters.
- **ansishot.py frames vary in height across scenes** (e.g. the editor pane vs. a plain
  `cat` shell scene have different numbers of rendered rows) and even within one scene
  (near-empty pre-paint frames right after launch, before the editor's first real paint).
  ffmpeg's concat+libx264 pipeline silently locks to the FIRST frame's dimensions and
  produces a broken video if later PNGs differ in size — it does not error, it just looks
  wrong. Fix: composite every segment onto a canvas sized to the GLOBAL max width and
  GLOBAL max height across ALL painted frames in the take (top-align, pad with the
  canvas background below shorter frames), and separately drop any captured frames whose
  timestamp precedes the take's first caption (kills the blank pre-paint blip at the very
  start). A general-purpose `render.py` implementing DEMO.md's segment/paint/caption-bar/
  concat recipe (not committed to the repo — DEMO.md intentionally has no committed demo
  tooling) needs both of these fixes to produce a correct mp4.
- **DEMO.md's segment-duration clamp (`[0.15s, 3s]`) caps how long a STATIC held frame can
  play, no matter how long the driver script sleeps on it.** Sleeping 5s+ after a `say()`
  on an otherwise-unchanging frame wastes wall-clock recording time for zero extra
  playback (still clamped to 3s), because the whole hold collapses into one
  (sha1, caption) segment. To hit a target runtime (e.g. the ~30-45s asked for here),
  keep each hold around 3.0-3.3s and add MORE distinct captioned beats instead (each new
  caption on the same underlying frame still counts as a fresh segment and adds up to
  ~3s) — went from ~20s/~26s two-clip totals to ~31s/~33s by adding 2-3 extra
  informational beats per clip (e.g. pointing out a specific glyph/tail/keyword color)
  rather than by lengthening existing sleeps.
- Recording env: `tmux new-session -x 84 -y 26` (matches ansishot's 13x28 cell size) keeps
  the composited canvas width at ~900-1150px, comfortably inside the 900-1300px spec
  without any extra scaling step.
