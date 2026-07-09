# NodeMods — the API

A mod is one JS program evaluated in its own sandboxed runtime at editor
start (and reloaded when /type opens or an agent turn ends). It registers
node types and/or chip kinds. The JS is trusted and may shell out.

## Where mods live

`~/.config/lflow/mods/`:

- `<key>.js` — a flat mod; the filename is the node type key.
- `<key>/` — a git-installed mod: `mod.json` manifest + entry JS.
- A `.disabled` suffix on either form turns the mod off (space in /type does
  the rename; ctrl+d deletes).

A mod repo (for `lflow node install <git-url>`) is:

```
lflow-<key>/
├── mod.json     {"name": "<key>", "description": "…", "entry": "<key>.js", "version": "0.1.0"}
├── <key>.js     the program
└── README.md
```

## Registering a node type

```js
lflow.registerType({
    key: "dice",            // the nodes.type string — must match the filename
    label: "Dice",          // the /type picker label
    sign: "⚂ ",             // optional inline prefix sign
    inlineEditable: true,   // false → the node text is read-only inline

    // hooks — all optional, all receive a read-only node object:
    // {uuid, name, type, color, addedOn (UnixNanos string), completed,
    //  collapsed, children (count)}
    glyph:     function (node) { return ["⚂", node.color || "yellow"]; }, // [char, color]
    baseColor: function (node) { return node.color || "dim"; },           // body foreground
    prefix:    function (node) { return lflow.style("(x) ", "dim"); },    // styled prefix before the body
    render:    function (node, name) { return name.toUpperCase(); },      // full body override
    muteFrom:  function (name) { return name.indexOf(" · "); },           // mute the tail from this index (-1 = none)
    run:       function (node) { return "echo hi"; },                     // alt+r: return a shell command to stream
});
```

`run` output streams beneath the node and is EPHEMERAL — never persisted or
synced; alt+e opens the scrollable output viewer. Runnable types execute on
alt+r only, never automatically.

## Registering a chip kind

```js
lflow.registerChip({
    key: "stamp", marker: "◷", color: "cyan",
    display: function (value) { return "◷ " + value; },  // compact inline form
    expand:  function (value) { return value; },          // full value (bash/search see this)
});
```

## Helpers

- `lflow.style(text, color)` — wrap text in a color; colors: red, orange,
  yellow, green, cyan, blue, purple, gray, plus fg/dim/accent.
- `lflow.time(addedOn)` — UnixNanos string → "YYYY-MM-DD HH:MM".
- `lflow.exec(cmd)` — synchronous shell; returns {stdout, stderr, code}.
  Runs on the render path — keep it fast or don't call it per-frame.

## Rules

- A compiled-in type key cannot be shadowed — mods extend, never override.
- A node of a mod type is a NORMAL node; removing or disabling the mod makes
  it render as a plain bullet with its text intact. Never migrate or rewrite
  the user's nodes for a mod.
- A broken mod is listed with its error in /type and its nodes fall back to
  bullets — but write valid JS anyway: test hooks mentally against the node
  object shape above.
