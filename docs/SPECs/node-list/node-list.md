# Spec · `lflow node list --depth` fix

**Status:** draft · bug fix
**Affects:** `pkg/tui/cmd/list/list.go`

## Summary

`lflow node list` with **no argument** ignores `--depth` and always prints only the
root's direct children. Make the no-arg listing **respect `--depth`**, with the
default `-1` meaning **all nodes**, printing every node as an entry row.

## Current behavior (the bug)

- `--depth` defaults to `-1` ("unlimited") and is honored **only when a node
  argument is given** — that path renders the subtree via `outline.Render*`.
- With **no argument**, `newRun` calls `listRoots`, which lists only
  `GetChildren(RootUUID)` and **ignores `--depth` entirely** (`list.go:42-63`,
  `list.go:69-72`). So `lflow node list --depth 2` still shows only roots —
  effectively depth 1 — and `--depth -1` does **not** show all nodes.

Each root prints in a nice entry format already:

```
c2a6f8  Inbox                                    bullets · 2 nodes
757482  What (ง ˙˘˙ )ว                            bullets · 6 nodes
4a5480  Not Defterleri                           bullets · 12 nodes
```

`<shortid>  <name>  <type> · <subtree count>` — id dim, meta dim
(`CountSubtree` + `resolve.CountNoun`).

## Desired behavior

- No-arg `list` **respects `--depth`**; default `-1` = **all nodes** (the whole
  forest), each printed as an entry row in the format above.
- `--depth 1` = roots only (today's behavior). `--depth 2` = roots + their children.
  `--depth -1` (default) = the entire tree.
- Indent the **name** by depth (2 spaces per level) so hierarchy reads; keep the id
  column flush-left and the `type · N nodes` meta on the right. *(OPEN: indent vs
  flat.)*

```
c2a6f8  Inbox                       bullets · 2 nodes
  ab12cd  triage rules              bullets · 1 node
757482  What (ง ˙˘˙ )ว              bullets · 6 nodes
  ...
```

## Implementation

- Replace the no-arg path: walk from `RootUUID` to `--depth` (`-1` = unlimited),
  printing each node via the existing entry formatter, indented by depth. Root
  children are level 1.
- Reuse `CountSubtree` + the dim id/name/meta format from `listRoots`.
- The argument path (subtree render) already honors `--depth`; leave it as is.

## Open questions

- Indented entries vs flat list.
- Subtree count = full descendants (current `CountSubtree`) vs direct children.

## Implementation checklist

- [ ] Generalize `listRoots` into a depth-bounded recursive walk respecting `opts.depth`.
- [ ] Keep default `-1` = all nodes; verify `--depth 1` reproduces today's roots-only output.
- [ ] Indent names by depth; keep id + meta columns aligned.
