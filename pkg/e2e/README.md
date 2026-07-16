# lflow editor e2e harness

A bash + tmux end-to-end regression suite that drives the **real** lflow binary
through its inline editor and asserts against the rendered terminal pane. It
replaces the old Go-based editor e2e tests.

## Isolation model

Each `test-*.sh` runs lflow in its **own** tmux session, against a throwaway
`:memory:` SQLite DB under an isolated `$HOME`. Because `:memory:` is
per-process, the starting tree is built by **typing in the editor**, never by
seeding from a separate process.

For the rare reopen/persistence test, call `use_persist_db` **before** `launch`
to switch the DB to a temp **file** so a relaunch keeps data; then use `reopen`.

The harness opens an empty in-memory outline via `lflow node open` (no node
arg), waits 1.2s for the first paint, then synchronizes with `wait_for` against
the live pane (poll, don't sleep). An `EXIT` trap always kills the session and
removes the temp HOME.

## Writing a test

```bash
#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; source "$DIR/lib.sh"
setup; launch
type "a"; send Enter; type "b"; send Tab; send Enter; type "c"
wait_for "○ c"
# ...drive the repro, assert the CORRECT (fixed) behavior...
assert_no_crash
pass
```

### Harness API (`lib.sh`)

- `setup` — isolated HOME + `:memory:` settings.json + unique session name
  (override window size by setting `WIN_W` / `WIN_H` before the call).
- `use_persist_db` — (before launch) use a temp file DB so reopen persists.
- `launch` — start the editor in a detached tmux session, settle 1.2s.
- `reopen` — kill + relaunch the same session/HOME (needs `use_persist_db`).
- `send <keys...>` — named tmux keys (`Enter`, `Tab`, `BSpace`, `Up`, `Down`,
  `Left`, `Right`, `End`, `M-r`, `M-e`, `C-s`, `Escape`, ...).
- `type "<text>"` — literal text.
- `snapshot` — echo the pane plain-text.
- `wait_for "<sub>" [timeout_s]` — poll until the pane contains `<sub>` (5s default).
- `assert_contains` / `assert_not_contains` / `assert_no_crash`.
- `pass` / `fail "<msg>"`.

### Rendering cheatsheet

```
○ name                   plain bullet
● name · N children      collapsed
◆ name · mirror          a mirror
├─ / ╰─                  child tree prefixes
◌                        a Temporary Domain node
$ cmd                    bash node
⌕ G q · N hits             query node
 <breadcrumb> · pos/total[ · state]   status bar (also the divider above ◌)
```

## Running

Run the whole suite (builds the binary once, sets `LFLOW_BIN`):

```bash
scripts/test.sh
```

Run a single test (reuses a prebuilt binary if `LFLOW_BIN` is set or
`/tmp/lflow-e2e-bin/lflow` exists; otherwise builds it):

```bash
bash pkg/e2e/test-empty-node-in-a-b-c.sh
```
