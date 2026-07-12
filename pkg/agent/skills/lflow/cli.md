# The lflow CLI

Every command is one-shot and pipe-friendly. Node arguments accept a uuid, a
uuid prefix, or free text (fuzzy-matched against names); `--strict` prints the
candidate matches instead of acting on the best one.

## Find nodes — `lflow node grep [text]`

```
$ lflow node grep retry
id        name                                      kids  type
d3f532    retry only transient failures                0  bullets
56dccc    importer retries                             1  bullets
```

Flags: `--all` includes completed nodes; `--type <t>` filters by node type
(bullets, todo, log, h1..h3, code, quote, json, bash, query, voice, image,
agent, wf, or any installed mod key).

## Read a subtree — `lflow node list [node]`

```
$ lflow node list                       # top level: id, name, type, size
56dccc  importer retries                bullets · 2 nodes

$ lflow node list importer              # a subtree as markdown
$ lflow node list importer --format json    # {uuid, name, type, children: [...]}
$ lflow node list importer --depth 2        # cap the depth
```

`--format json` is the structural form — uuid, name, type, children — use it
when you need ids to act on.

## Create — `lflow node add [text]`

```
$ lflow node add "retry only transient failures" --parent importer
$ lflow node add "check exit codes" --parent importer --type todo --top
```

Placement follows the parent's priority: a priority-up parent takes new nodes
on TOP (its children read newest-first), a down parent appends at the bottom.
`lflow mv` follows the same rule when no explicit position is given.

Flags: `--parent <node>` (default root), `--type <t>`, `--top` prepends,
`--note <text>`, styling (`--bold --italic --underline --strike
--color red|orange|yellow|green|cyan|blue|purple|gray`, or `--style
"bold,color:red"`), `--raw` stores text verbatim (skips #tag/date/link → chip
conversion).

## Edit — `lflow node edit <node>`

```
$ lflow node edit d3f532 --state complete
$ lflow node edit d3f532 --name "retry transient failures only" --type todo
$ lflow node edit d3f532 --note "curl exits 52 and 56" --color yellow
```

Flags: `--name`, `--note`, `--type <t>`, `--state complete|uncomplete`,
`--readonly[=false]`, the same styling flags as add, `--raw` with `--name`.

## Install a mod — `lflow node install <git-url>`

```
$ lflow node install https://github.com/yippiez/lflow-log
→ installed log 0.1.0 · timestamped log entries
```

Clones the repo into `~/.config/lflow/mods/<name>/`; re-running updates.

## Composing

Find, then act — grep gives the id, list --format json gives structure:

```
$ lflow node grep "release notes"                # → id 8a41b2
$ lflow node list 8a41b2 --format json           # read the subtree
$ lflow node add "draft the changelog" --parent 8a41b2 --type todo
$ lflow node edit 8a41b2 --state complete
```
