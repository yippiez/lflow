# lflow commands

Lflow operates on outline **nodes**. Most commands take a node reference, which is
resolved by full-text search, a node id, or an id prefix; they act on the single
best match unless `--strict` is given. Completed nodes always resolve: a reference
you typed is a reference you meant.

```
lflow node open|list|add|move|remove|edit
lflow export                      dump the whole forest
lflow server login|logout|sync    self-hosted lflow-server
lflow version
```

The SQLite database lives at the standard data location; set `dbPath` in
`~/.config/lflow/lflowrc` to relocate it — there is no flag. `--help` on any
command shows its help; there is no help command.

## lflow node open

```
lflow node open [node]
```

Open the inline editor on the best match for a query or id. With no argument,
open the root.

```
lflow node open                      # the whole outline
lflow node open "experiment results"
lflow node open 31b450               # id prefix
```

## lflow node list

```
lflow node list [node]
```

Print a node's subtree to stdout. With no argument, list the top-level nodes with
their ids. Completed nodes are always included.

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `md` | output format: `md`, `text` or `json` |
| `--depth` | `-1` | maximum depth, `-1` means unlimited |
| `--strict` | false | list matches instead of acting on the best match |

```
lflow node list
lflow node list "experiment results" --depth 2
lflow node list "experiment results" --format json | jq -r '.children[0].name'
```

## lflow node add

```
lflow node add [text]
```

Add nodes under a parent — the always-present root by default, `--parent` targets
a node deeper in the tree. Text comes from positional arguments or piped stdin,
where **every line becomes one child node**.

| Flag | Default | Description |
| --- | --- | --- |
| `--parent <node>` | "" | parent node, defaults to root |
| `--note` | false | append the text to the parent's note instead of creating children |
| `--top` | false | prepend instead of append |
| `--strict` | false | list matches instead of acting on the best match |

```
lflow node add "reading list"
lflow node add --parent "reading list" "ddia"
make bench 2>&1 | lflow node add --parent "experiment results"
echo "context" | lflow node add --parent "experiment results" --note
```

## lflow node move

```
lflow node move <node> <new-parent>
```

Move a node and its subtree under another node, placed last by default.

| Flag | Default | Description |
| --- | --- | --- |
| `--top` | false | place as the first child instead of the last |
| `--after <sibling>` | "" | place after the given sibling, which must be a child of the new parent |
| `--strict` | false | list matches instead of acting on the best match |

Moving a node into itself or into its own subtree is rejected.

## lflow node remove

```
lflow node remove <node>
```

Delete a node and its subtree. The delete is tombstoned, so it is pushed on sync.

| Flag | Default | Description |
| --- | --- | --- |
| `-f`, `--force` | false | skip confirmation |
| `--strict` | false | list matches instead of acting on the best match |

## lflow node edit

```
lflow node edit <node>
```

Edit a node's properties. At least one of the property flags is required.

| Flag | Default | Description |
| --- | --- | --- |
| `--state` | "" | `complete` or `uncomplete` |
| `--name` | "" | rename the node |
| `--note` | "" | replace the node's note |
| `--type` | "" | `bullets`, `todo`, `h1`, `h2`, `h3`, `code` or `quote` |
| `--strict` | false | list matches instead of acting on the best match |

```
lflow node edit "attempt 2" --state complete
lflow node edit "attempt 2" --name "attempt 3" --type todo
```

## lflow export

```
lflow export
```

Export the whole local forest, completed nodes included, to stdout.

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `json` | output format: `json` or `md` |

## lflow server

The self-hosted `lflow-server` commands.

### lflow server login

```
lflow server login
```

Log in to the lflow server. Prompts for email and password if not provided.

| Flag | Default | Description |
| --- | --- | --- |
| `-u`, `--username <email>` | "" | email address for authentication |
| `-p`, `--password <pw>` | "" | password for authentication |
| `--apiEndpoint <url>` | "" | API endpoint to connect to, defaults to the config value |

### lflow server logout

```
lflow server logout
```

Log out from the server.

### lflow server sync

```
lflow server sync
```

Sync nodes with the lflow server, requires `lflow server login`.

| Flag | Default | Description |
| --- | --- | --- |
| `-f`, `--full` | false | perform a full sync instead of an incremental one |
| `--dry-run` | false | show what would be synced without making changes |
| `--apiEndpoint <url>` | "" | API endpoint to connect to, defaults to the config value |

## lflow version

```
lflow version
```

Print the version.
