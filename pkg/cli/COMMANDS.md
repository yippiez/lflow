# lflow commands

- [add](#lflow-add)
- [view](#lflow-view)
- [edit](#lflow-edit)
- [remove](#lflow-remove)
- [find](#lflow-find)
- [sync](#lflow-sync)
- [login](#lflow-login)
- [logout](#lflow-logout)

## lflow add

Add a new note.

```
lflow add <book>
```

```
lflow add linux
```

```
lflow add linux -c "find - recursively walk the directory"
```

## lflow view

View notes.

```
lflow view
```

```
lflow view golang
```

```
lflow view 12
```

## lflow edit

Edit a note or a book.

```
lflow edit 12
```

```
lflow edit 12 -c "New Content"
```

```
lflow edit js
```

```
lflow edit js -n "javascript"
```

## lflow remove

Remove a note or a book.

```
lflow remove 1
```

```
lflow remove js
```

## lflow find

Search notes.

```
lflow find rpoplpush
```

```
lflow find "building a heap"
```

```
lflow find "merge sort" -b algorithm
```

## lflow sync

Sync notes with the server.

## lflow login

Login to the lflow server.

## lflow logout

Logout from the lflow server.
