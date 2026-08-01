# Verifier memory

## Environment quirks (this sandbox)

- `ast-grep` is NOT preinstalled. Fix: `python3 -m pip install ast-grep-cli`
  (installs `/usr/local/bin/ast-grep` and `/usr/local/bin/sg`). Do NOT trust
  `which sg` alone — `/usr/bin/sg` exists as a symlink to `newgrp` (the unix
  "switch group" tool) and will look like a hit; check for `ast-grep` on PATH
  specifically, or run `ast-grep --version`.
- `ffmpeg` is NOT preinstalled and plain `apt-get install ffmpeg` can 404 on
  stale `noble-updates` package lists (libva2/libcaca0/mesa-vdpau-drivers
  version mismatches). Fix: `apt-get update` first (refreshes the noble-updates
  index), then `apt-get install -y --no-install-recommends ffmpeg` (skips the
  vaapi/pulseaudio/X11 recommends chain — installs cleanly and is all a
  headless recording needs).
- `python3 -c "import PIL"` fails until `pip install pillow` (needed by
  `scripts/ansishot.py`). Not preinstalled either.
- As of the 2026-08-01 re-verification pass, `ast-grep`/`ffmpeg`/pillow were
  all *already* installed at the start of the session (persisted from a prior
  pass in this same container) — always check first (`ast-grep --version`,
  `which ffmpeg`, `python3 -c "import PIL"`) before re-installing.
- `go test --tags fts5 ./...` for an unchanged tree returns a lot of `(cached)`
  results — harmless, but when verifying a NEW test file always add
  `-count=1` (or `-run <TestName> -v`) at least once to prove it actually ran
  rather than trusting a cache hit.
- Bash tool calls are independent shells but background (`disown`ed)
  processes started in one call keep running and are visible/killable from
  later calls — `pgrep -af <pattern>` to find them, `kill -9 <pid>`. Job
  control (`kill %1`) does NOT work across calls (no job table carries over) —
  it just errors silently ("no such job").
- Do not `pkill -f "<name>"` where `<name>` also appears in your own
  in-flight command line (e.g. a scratch dir path like `/tmp/lflow-demo`) —
  `pgrep -af`/`pkill -f` match against the *whole* command string, including
  the wrapper shell that is running your `pkill` itself, and killing your own
  shell mid-command produces a bare `Exit code 144` with no other output.
  Kill by explicit pid list (from a prior `pgrep -af`) instead of a fuzzy `-f`
  pattern when the pattern might match your own invocation.

## Known e2e flake signature (pre-existing, not a regression)

As of 2026-08-01 (base commit 17b99b1), `bash scripts/test.sh` fails the same
12/32 scripts on a totally clean checkout with NO changes applied — confirmed
by `git stash -u` (must include `-u`, these add untracked files) and rerunning:

  test-bash-node-shows-dollar-and-spinner.sh
  test-bring-moves-node-here.sh
  test-home-end-visual-line.sh
  test-mirror-cursor-stays-local.sh
  test-mirror-outdent-escapes-source.sh
  test-mirror-shows-live-source-edits.sh
  test-section-keeps-one-node.sh
  test-slash-escape-removes-trigger-keeps-status.sh
  test-status-bar-is-temp-divider.sh
  test-temp-panel-always-visible.sh
  test-voice-nil-map-panic-on-load.sh
  test-zoom-into-mirror-shows-source-children.sh

All fail as timeouts waiting for expected pane text, or "expected pane to NOT
contain: ◌" — looks like general tmux/timing sluggishness in this container
(capture/settle timing in `pkg/e2e/lib.sh` too tight for this box's CPU), not
anything code-related. `test-temp-persists-across-reopen.sh` SKIPs here too:
sqlite3 CLI isn't installed (expected — DEMO.md already says the CLI lacks
fts5 and DB surgery must go through a `-tags fts5` Go program instead).
Before blaming a diff for e2e failures, `git stash -u` back to base and rerun
`scripts/test.sh` to check whether the same scripts fail identically — if so
it's this environment, not the change.

Reconfirmed unchanged on the 2026-08-01 re-verification pass (same commit
base, Line-node diff on top): identical 12 scripts failed, 20 passed. No new
failures from the Line-node/typeSource fix.

Also pre-existing on base (unrelated to any diff touching these files):
`ast-grep scan` reports 1 error (`no-direct-sql-open` in
`pkg/tui/daemon/store.go:102`, a direct `sql.Open` call) and a handful of
`capitalize-error-strings` warnings in `pkg/tui/wire/wire.go` and
`pkg/tui/editor/agentstore.go`. Confirmed via the same stash-and-rerun trick,
and reconfirmed identical on the 2026-08-01 re-verification pass.
Don't let these block a PASS unless the diff actually touches those lines.

## Bugs found while verifying (real, not environment)

### 2026-08-01 — Line node: onType-opened picker mode gets stomped — FIXED

Original finding: `typeSource.onSelect` in `pkg/tui/editor/picker_sources.go`
(the `/type` picker's Enter handler) unconditionally ran `m.mode =
modeOutline` as its last statement, AFTER calling each target's `onType` hook.
Every onType hook that existed before the Line node only changed fold/view
state and never touched `m.mode`, so this never mattered — the Line node's
onType (`m.openCharacterPicker(it)`) was the first that needed to leave a
picker OPEN in a new mode (`modeCharacterPick`), and the trailing reset
immediately closed it back to `modeOutline` before the user ever saw it. Net
effect: `/type` -> `line` -> Enter dropped straight into plain inline text
editing instead of the character picker; typed characters like "ALICE" landed
as literal node body text instead of filtering the picker. The Line node's own
unit test didn't catch it because it called the `onType` hook directly,
bypassing `typeSource.onSelect` entirely.

**Fix applied and reverified same day**: the reset in `typeSource.onSelect` is
now guarded — `if m.mode == modeType { m.mode = modeOutline }` — so it only
snaps back when no `onType` hook changed the mode on its own; Table's onType
(which never touches `m.mode`) is unaffected. `line_test.go`'s
`TestLineCharacterAssignAndRender` was hardened to call the real
`typeSource{}.onSelect(m, pickerItem{value: database.TypeLine})` entry point
(not the bare hook) and assert `m.mode == modeCharacterPick` afterward, so it
now actually exercises the regression path. Reverified via `go build`/`go
test --tags fts5 ./...` (including `-run TestLineCharacterAssignAndRender -v
-count=1`) and a full DEMO.md recording — a mid-take frame right after
selecting "Line" showed `character: type to search or write a new character`
(the picker header), not a live text cursor, proving the fix holds under the
real `/type` picker Enter path, not just the direct-hook-call unit test.

### Demo-staging gotcha (not a bug): character-color state outlives its node

`characterColors` (`pkg/tui/editor/line.go`) is keyed by **character name**,
not node uuid, and is never cleaned up when the Line node(s) that reference a
character are deleted. Recording take 1 of this demo created "ALICE" and
recolored her to cyan, then `node remove -f`'d that node and re-seeded a fresh
empty node in the *same* sandbox env/DB for take 2 — but typing "ALICE" again
immediately showed her already cyan (the stale `characterColors` row was still
in the DB), and the recolor picker's `initialSel` started already on "cyan"
instead of "none", so a fixed "Down x5" script overshot to "blue". Not a
product bug — a demo artifact of reusing one sandbox DB across takes. Fix:
when re-recording a take that touches named/keyed global state (characters,
tags, etc.), `rm -rf` the whole `env-<name>` sandbox dir and reseed from
scratch rather than deleting individual nodes.

## Demo recording notes

- The `sandbox` wrapper script + tmux capture-loop + `ansishot.py` + ffmpeg
  concat recipe in DEMO.md works as documented once ast-grep/ffmpeg/pillow are
  installed (see quirks above). `tmux capture-pane -p -e` cleanly captures the
  Line-node UI's 24-bit color codes.
- Color palette order note: `styleColorOrder` / `style.Colors` =
  `red, orange, yellow, green, cyan, blue, purple, gray`, and the recolor
  picker prepends `none` (default) as index 0 — so from a freshly-created
  (uncolored) character, reaching "cyan" is 5 x Down
  (none -> red -> orange -> yellow -> green -> cyan). This assumes the
  character truly has no prior color in the DB — see the demo-staging gotcha
  above.
- Always dry-run the interaction sequence against the actual source (registry
  hooks, picker onSelect/onKey) before scripting `k`/`type_slow` calls — don't
  just trust the feature summary. This run caught a real bug precisely
  because the tmux capture showed keystrokes landing in the wrong place
  ("ALICE" as literal node text) instead of the picker filtering — worth
  reading a frame or two mid-recording rather than only checking the final
  frame.
- DEMO.md's segmenter clamps each collapsed segment's duration to **[0.15s,
  3s]** regardless of how long you `sleep` after a `k`/`say` in the driver
  script — lengthening a single scene's hold past ~3s of wall-clock time buys
  you nothing once you're already at the cap. To meaningfully lengthen a take
  toward the "~30-60s" guideline, add more distinct scenes/captions, not
  longer sleeps on existing ones. A clean 11-caption, single-scene take like
  this one naturally lands around 28-30s — treat that as fine; the range in
  DEMO.md is a guideline, not a hard gate.
- `node add ""` (empty string arg) seeds a genuinely empty bullets node — use
  this instead of a placeholder title like "new dialogue node" when the demo
  is going to type new text into that same node right after a `/type`
  conversion (the node's pre-existing text is NOT cleared by `/type`, and the
  cursor can land at the *start* of it, producing a garbled
  "typed-text-then-old-title" frame instead of clean dialogue text).
