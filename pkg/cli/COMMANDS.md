# lflow commands

Lflow operates on outline **nodes**. Most commands take a node reference, which is
resolved by full-text search (or a node id); they act on the single best match
unless `--strict` is given. `--all` includes completed nodes when resolving.

- [add / append](#lflow-add--lflow-append)
- [list](#lflow-list)
- [find](#lflow-find)
- [edit](#lflow-edit)
- [mv](#lflow-mv)
- [rm](#lflow-rm)
- [complete / uncomplete](#lflow-complete--lflow-uncomplete)
- [export](#lflow-export)
- [sync](#lflow-sync)
- [login](#lflow-login)
- [logout](#lflow-logout)
- [wf](#lflow-wf)
- [version](#lflow-version)

A global `--dbPath <path>` flag (accepted before or after the subcommand) overrides
the SQLite database location.

## lflow add / lflow append

```
lflow add <node> [text]
lflow append <node> [text]
```

Add child nodes under a node. `append` is an alias for the same operation. Text can
be passed as positional arguments or piped on stdin, where **every line becomes one
child node**. Aliases: `a`, `new` (add); `ap` (append).

| Flag | Default | Description |
| --- | --- | --- |
| `--note` | false | append the text to the node's note instead of creating children |
| `--top` | false | prepend instead of append |
| `--strict` | false | list matches instead of acting on the best match |
| `--all` | false | include completed nodes when resolving |
| `--parent <node>` | false | create a new root node named `<node>` (no parent resolution) |

```
lflow add --root "reading list"
lflow add "experiment results" "attempt 3"
make bench 2>&1 | lflow append "experiment results"
echo "context" | lflow append "experiment results" --note
```

## lflow list

```
lflow list [node]
```

Print a node's subtree to stdout. Aliases: `ls`, `l`.

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `md` | output format: `md`, `text` or `json` |
| `--depth` | `-1` | maximum depth (`-1` = unlimited) |
| `--completed` | false | include completed nodes |
| `--roots` | false | list top-level nodes |
| `--strict` | false | list matches instead of acting on the best match |
| `--all` | false | include completed nodes when resolving |

```
lflow list "experiment results" --depth 2
lflow list "experiment results" --format json | jq -r '.children[0].name'
lflow list --roots
```

## lflow find

```
lflow find <query>
```

Find a node and open the inline editor on the best match. Alias: `f`.

| Flag | Default | Description |
| --- | --- | --- |
| `--print` | false | print the outline instead of opening the editor |
| `--strict` | false | list matches instead of opening the best one |
| `--all` | false | include completed nodes |
| `--id` | false | print only the node id of the best match |

```
lflow find "experiment results"
lflow find "experiment results" --print
lflow find exp --strict
```

## lflow edit

```
lflow edit <node>
```

Open the inline editor directly on a node. Alias: `e`.

| Flag | Default | Description |
| --- | --- | --- |
| `--strict` | false | list matches instead of opening the best one |
| `--all` | false | include completed nodes |

## lflow mv

```
lflow mv <node> <new-parent>
```

Move a node (and its subtree) under another node. Alias: `move`.

| Flag | Default | Description |
| --- | --- | --- |
| `--top` | false | place as the first child |
| `--bottom` | true | place as the last child (default) |
| `--after <sibling>` | "" | place after the given sibling (must be a child of the new parent) |
| `--strict` | false | list matches instead of acting on the best match |
| `--all` | false | include completed nodes |

Moving a node into itself or into its own subtree is rejected.

## lflow rm

```
lflow rm <node>
```

Delete a node and its subtree (tombstoned, so the delete is pushed on sync).
Aliases: `remove`, `d`, `delete`.

| Flag | Default | Description |
| --- | --- | --- |
| `-f`, `--force` | false | skip confirmation |
| `--strict` | false | list matches instead of acting on the best match |
| `--all` | false | include completed nodes |

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

Export the whole local forest to stdout.

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `json` | output format: `json` or `md` |
| `--completed` | true | include completed nodes |

## lflow sync

```
lflow sync
```

Sync nodes with the lflow server (requires `lflow login`). Alias: `s`.

| Flag | Default | Description |
| --- | --- | --- |
| `-f`, `--full` | false | perform a full sync instead of an incremental one |
| `--dry-run` | false | show what would be synced without making changes |
| `--apiEndpoint <url>` | "" | API endpoint to connect to (defaults to the config value) |

## lflow login

```
lflow login
```

Log in to the lflow server. Prompts for email and password if not provided.

| Flag | Default | Description |
| --- | --- | --- |
| `-u`, `--username <email>` | "" | email address for authentication |
| `-p`, `--password <pw>` | "" | password for authentication |
| `--apiEndpoint <url>` | "" | API endpoint to connect to (defaults to the config value) |

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

Log in to workflowy (or store a session id directly).

| Flag | Default | Description |
| --- | --- | --- |
| `--session <id>` | "" | store a workflowy sessionid directly (for 2FA accounts) |
| `--base-url <url>` | "" | override the workflowy endpoint (testing / self-hosted) |

### lflow wf mirror

```
lflow wf mirror <url|wf-id>
```

Anchor a workflowy node into the local tree. The argument may be a workflowy URL
(e.g. `https://workflowy.com/#/abc123def456`) or a node id.

| Flag | Default | Description |
| --- | --- | --- |
| `--into <node>` | "" | local parent node (default: a new root) |

### lflow wf list

```
lflow wf list
```

List workflowy mirrors (shows last sync time and node count).

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
</content>
