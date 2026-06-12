# lflow commands

Lflow operates on outline **nodes**. Most commands take a node reference, which is
resolved by full-text search, a node id, or an id prefix; they act on the single
best match unless `--strict` is given. Completed nodes always resolve: a reference
you typed is a reference you meant.

- [add / append](#lflow-add--lflow-append)
- [list](#lflow-list)
- [open](#lflow-open)
- [mv](#lflow-mv)
- [rm](#lflow-rm)
- [complete / uncomplete](#lflow-complete--lflow-uncomplete)
- [export](#lflow-export)
- [sync](#lflow-sync)
- [login](#lflow-login)
- [logout](#lflow-logout)
- [wf](#lflow-wf)
- [version](#lflow-version)

A global `--dbPath <path>` flag, accepted before or after the subcommand, overrides
the SQLite database location.

## lflow add / lflow append

```
lflow add [text]
lflow append <node> [text]
```

`add` creates top-level nodes under the always-present root; `--parent` targets a
node deeper in the tree. `append` is the same operation with the parent passed
positionally. Text comes from positional arguments or piped stdin, where **every
line becomes one child node**. Aliases: `a`, `new` for add; `ap` for append.

| Flag | Default | Description |
| --- | --- | --- |
| `--parent <node>` | "" | parent node, defaults to root — add only |
| `--note` | false | append the text to the node's note instead of creating children |
| `--top` | false | prepend instead of append |
| `--strict` | false | list matches instead of acting on the best match |

```
lflow add "reading list"
lflow add --parent "reading list" "ddia"
make bench 2>&1 | lflow append "experiment results"
echo "context" | lflow append "experiment results" --note
```

## lflow list

```
lflow list [node]
```

Print a node's subtree to stdout. With no argument, list the top-level nodes with
their ids. Completed nodes are always included. Aliases: `ls`, `l`.

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `md` | output format: `md`, `text` or `json` |
| `--depth` | `-1` | maximum depth, `-1` means unlimited |
| `--strict` | false | list matches instead of acting on the best match |

```
lflow list
lflow list "experiment results" --depth 2
lflow list "experiment results" --format json | jq -r '.children[0].name'
```

## lflow open

```
lflow open [node]
```

Open the inline editor on the best match for a query or id. With no argument,
open the root. Aliases: `o`, `e`, `f`, `edit`, `find`.

```
lflow open                      # the whole outline
lflow open "experiment results"
lflow open 31b450               # id prefix
```

## lflow mv

```
lflow mv <node> <new-parent>
```

Move a node and its subtree under another node, placed last by default.
Alias: `move`.

| Flag | Default | Description |
| --- | --- | --- |
| `--top` | false | place as the first child instead of the last |
| `--after <sibling>` | "" | place after the given sibling, which must be a child of the new parent |
| `--strict` | false | list matches instead of acting on the best match |

Moving a node into itself or into its own subtree is rejected.

## lflow rm

```
lflow rm <node>
```

Delete a node and its subtree. The delete is tombstoned, so it is pushed on sync.
Aliases: `remove`, `d`, `delete`.

| Flag | Default | Description |
| --- | --- | --- |
| `-f`, `--force` | false | skip confirmation |
| `--strict` | false | list matches instead of acting on the best match |

## lflow complete / lflow uncomplete

```
lflow complete <node>
lflow uncomplete <node>
```

Mark a node completed / not completed.

| Flag | Default | Description |
| --- | --- | --- |
| `--strict` | false | list matches instead of acting on the best match |

## lflow export

```
lflow export
```

Export the whole local forest, completed nodes included, to stdout.

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `json` | output format: `json` or `md` |

## lflow sync

```
lflow sync
```

Sync nodes with the lflow server, requires `lflow login`. Alias: `s`.

| Flag | Default | Description |
| --- | --- | --- |
| `-f`, `--full` | false | perform a full sync instead of an incremental one |
| `--dry-run` | false | show what would be synced without making changes |
| `--apiEndpoint <url>` | "" | API endpoint to connect to, defaults to the config value |

## lflow login

```
lflow login
```

Log in to the lflow server. Prompts for email and password if not provided.

| Flag | Default | Description |
| --- | --- | --- |
| `-u`, `--username <email>` | "" | email address for authentication |
| `-p`, `--password <pw>` | "" | password for authentication |
| `--apiEndpoint <url>` | "" | API endpoint to connect to, defaults to the config value |

## lflow logout

```
lflow logout
```

Log out from the server.

## lflow wf

Workflowy integration: anchor workflowy nodes into the local tree. The `wf` command
has subcommands:

### lflow wf login

```
lflow wf login
```

Log in to workflowy, or store a session id directly.

| Flag | Default | Description |
| --- | --- | --- |
| `--session <id>` | "" | store a workflowy sessionid directly, for 2FA accounts |
| `--base-url <url>` | "" | override the workflowy endpoint for testing or self-hosting |

### lflow wf mirror

```
lflow wf mirror <url|wf-id>
```

Anchor a workflowy node into the local tree. The argument may be a workflowy URL
like `https://workflowy.com/#/abc123def456` or a node id.

| Flag | Default | Description |
| --- | --- | --- |
| `--into <node>` | "" | local parent node, defaults to a new root |

### lflow wf list

```
lflow wf list
```

List workflowy mirrors with last sync time and node count.

### lflow wf pull / lflow wf push

```
lflow wf pull [mirror]
lflow wf push [mirror]
```

Reconcile mirrors with workflowy. Both subcommands run sync in **both directions**;
the name only reflects emphasis. With no argument, all mirrors are synced; with a
mirror reference, only that anchor. Conflicts are resolved in workflowy's favour and
the overwritten local value is journaled to `wf-journal.log`.

### lflow wf unmirror

```
lflow wf unmirror <mirror>
```

Detach a workflowy mirror. You must pass exactly one of:

| Flag | Default | Description |
| --- | --- | --- |
| `--keep` | false | keep the local copy |
| `--drop` | false | delete the local copy |

## lflow version

```
lflow version
```

Print the version number.
