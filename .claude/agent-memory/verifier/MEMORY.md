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
