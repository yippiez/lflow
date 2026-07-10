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

## Custom UI — the `view` (alt+e)

A `view` gives a mod the FULL inline expanded surface — its own multi-line
render, key capture, state, and async effects — so a mod is as rich as a
built-in type (image, json). It draws bands BENEATH the node in the outline
flow (never an alt-screen); Go owns the tree rail, the indent, width clipping,
and the scroll window. It is an Elm loop:

```js
lflow.registerType({
    key: "barchart", label: "Barchart", inlineEditable: true,
    render: function (node, name) { return name + "  ▂▄█"; }, // collapsed one-liner
    view: {
        // init: seed per-node state; may rehydrate durable state via getData.
        init:   function (node) { return lflow.getData(node.uuid) || { hi: 0 }; },
        // render: return the FULL list of styled band strings (Go windows it).
        //   ctx = { width, focused, scroll, winH } — width is the usable inner width.
        render: function (node, s, ctx) { return ["line one", "line two"]; },
        // lines: optional total-line count (for scroll); else derived from render.
        lines:  function (node, s, ctx) { return 2; },
        // key: handle a keystroke. Return { state?, effect? } to update/act, or
        //   false/undefined to fall through (esc/ctrl+c always close centrally).
        key:    function (node, s, ctx, k) {
                    if (k === "down") return { state: { hi: s.hi + 1 } };
                    if (k === "r")    return { state: s, effect: { kind: "exec", cmd: "date" } };
                    return false;
                },
        // update: fold an effect's result (msg) back into state; may raise another
        //   effect to keep an animation/poll loop going.
        update: function (node, s, msg) { return { state: { out: msg.stdout } }; },
        // enter: optional; return false to decline focus, or { state?, effect?, focus? }.
        enter:  function (node, s) { return true; },
        // leave: persist durable state on esc (ephemeral state stays in memory).
        leave:  function (node, s) { lflow.setData(node.uuid, s); },
    },
});
```

### Effects — async without blocking the frame

`render` is pure and runs on the draw path — do NOT call `lflow.exec` there.
Side effects come only from `key`/`update`/`enter`, returned as a descriptor Go
runs off the event loop; its result arrives back in `update(node, state, msg)`:

- `{ kind: "exec", cmd }` → `msg = { kind: "exec", stdout, stderr, code }`
- `{ kind: "fetch", url }` → `msg = { kind: "fetch", status, body }`
- `{ kind: "tick", ms }`  → `msg = { kind: "tick" }` — one animation frame;
  return another `tick` from `update` to keep animating. Ticks are delivered
  only while the view is focused, so a loop can't animate a node you've left.
- `{ kind: "batch", effects: [ … ] }` → run several at once.

### Graphics — the canvas escape hatch

For pixel/graph/game UIs, paint an absolute truecolor cell grid and return it:

```js
var cv = lflow.canvas(ctx.width, rows);      // w × h cells
cv.set(x, y, "█", "#7fd4ff", "");            // (x, y, char, fg, bg) — hex/name/""
return cv.bands();                            // → band strings for render
```

### Layout kit (`lflow.text`)

Width-aware so you never miscount cells (wide runes, ANSI): `width(s)`,
`truncate(s, w)`, `pad(s, w, "left"|"right"|"center")`, `repeat(s, w)`,
`hrule(ch, w)`.

### State

- Ephemeral state (scroll, cursor, edit buffer) lives in memory — it survives a
  focus toggle and the mod reload after an agent turn, but not a restart.
- Durable state is `lflow.setData(uuid, obj)` / `lflow.getData(uuid)` — small
  JSON in `node_mod_data`, local-only (never synced), decoupled from the node
  row. Keep BLOBS out: shell out via `lflow.exec` to a `<uuid>`-keyed file.

See `examples/barchart.js` for a full working view (canvas + effect + persist).

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
