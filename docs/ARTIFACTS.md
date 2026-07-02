# Artifacts + tag design

lflow's node types move to an **artifact** model: a node type (or chip kind) can
be generated on demand — typically by a coding agent mid-note — installed at
runtime, used immediately, and forgotten later without ever breaking the notes
that used it. This document is the merge target for the artifact and tag work
across branches; every decision below was made deliberately.

## Decisions (interview log)

| Question | Decision |
|---|---|
| Plugin runtime | **Embedded JS via goja** (pure Go, in-process, no rebuild, agents write JS well) |
| SDK scope | **Node types + chip kinds, nothing else** — no global hooks, no keybindings, no platform sprawl |
| Storage | **`artifacts` table in lflow.db** — definitions travel with the outline; a note never loses its rendering |
| Capabilities | **Trusted, full access everywhere** — artifact JS may exec shell in any hook (single-user local tool) |
| Built-ins | **Only Log migrates** to a seeded artifact (the reference/dogfood artifact); all other types stay compiled Go |
| Picker | **Unified `/type` + fuzzy search** — built-ins first, then artifacts; `/artifacts` is the management view |
| Agent backend | **Pi coding-agent service over RPC/websocket**, driven by a new `pkg/tui/tag` package with session management |
| Session model | **Claude-Tag style**: an @mention creates a session bound to that node; the node's subtree is the thread; the agent posts replies as child nodes; later mentions in the thread continue the same session |
| Watch scope | **Active threads only.** A session sees its thread node's ancestor chain + subtree recursively — its own level and below, never siblings elsewhere. Mirror expansion is cycle-guarded |
| Agent output | **A dedicated basic node type** (`agent`): one node per message, red, ✦ glyph, plain text + chips only. Artifact creation is a separate, explicitly requested act |
| Service lifecycle | **Tied to the editor process** — closing the editor pauses in-flight sessions; session ids in the DB let a thread resume next open |

## Artifact = one JS file in the DB

```sql
CREATE TABLE artifacts (
    key        text PRIMARY KEY,  -- the node-type key, e.g. "log"
    label      text NOT NULL,     -- picker label
    version    integer NOT NULL DEFAULT 1,
    source     text NOT NULL,     -- the JS program
    created_by text NOT NULL DEFAULT 'user',  -- 'seed' | 'user' | agent name
    created_at integer NOT NULL DEFAULT 0,
    enabled    bool NOT NULL DEFAULT true
);
```

`nodes.type` stays a free string. An artifact whose key matches a node's type
takes over that type's editor behavior; a node whose artifact is disabled or
missing falls back to bullets (`typeOf`'s long-standing fallback), so nothing
ever crashes or blocks loading.

## The JS SDK (pkg/tui/editor/artifact.go)

Each enabled artifact is evaluated once at editor start in its own goja
runtime. The program calls exactly two registration functions:

```js
lflow.registerType({
    key: "log",                 // required, the nodes.type string
    label: "Log",               // required, the /type picker label
    sign: "",                   // optional inline prefix sign, e.g. "$ "
    inlineEditable: true,       // typing edits the body inline
    glyph: (node) => ["→", node.color || "dim"],       // [text, colorName]
    baseColor: (node) => node.color || "dim",          // body foreground
    prefix: (node) => lflow.style("(" + lflow.time(node.addedOn) + ") ", "dim"),
    muteFrom: (name) => name.indexOf(" · "),           // mute tail from index
    render: (node, name) => "…",                       // full body override (read-only types)
    run: (node) => "curl -s wttr.in?format=3",         // alt+r: a shell command to stream
});

lflow.registerChip({
    key: "stamp",
    marker: "◷",
    color: "cyan",
    display: (value) => "◷ " + value,   // compact inline text
});
```

- Hooks are plain JS functions called from the render/update path. **Trusted:**
  `lflow.exec(cmd)` (synchronous shell) is available inside any hook; a slow
  hook slows the frame — that is the user's own artifact's problem, not a
  sandbox's. All JS runs on the bubbletea goroutine, so there is no locking.
- `run` returns a shell command string; the Go side streams it through the
  same ephemeral run machinery as bash nodes (output under the node, alt+e
  viewer, never persisted — the run-output invariant holds for artifacts).
- Color names resolve through the active theme (`dim`, `red`, `yellow`,
  `green`, `accent`, `fg`), so artifacts follow `/theme` switches.

The Go bridge turns each registration into a regular `nodeType` descriptor
appended to the compiled-in registry — a single source of truth; `/type`,
glyphs and rendering treat both kinds identically.

## Log is the reference artifact

Migration seeds the `log` artifact (created_by='seed') implementing exactly the
old compiled-in behavior: → glyph tinted by /color, muted "(YYYY-MM-DD HH:MM)"
time chip, muted " · description" tail. The `TypeLog` Go constant remains (data
compatibility); the hardcoded render branches are gone. If you disable the log
artifact, log nodes degrade to plain bullets — by design.

## tag/: @mentions, sessions, the Pi bridge

```sql
CREATE TABLE agent_sessions (
    id         text PRIMARY KEY,   -- remote session id
    node_uuid  text NOT NULL,      -- thread root node
    agent      text NOT NULL,      -- e.g. "Pi"
    state      text NOT NULL DEFAULT 'idle',  -- idle | running | paused
    created_at integer NOT NULL DEFAULT 0,
    updated_at integer NOT NULL DEFAULT 0
);
```

- Typing `@` opens the agent completer (configured agents). Committing the
  node (Enter) with a fresh mention **is** the send — the keyboard gesture
  stands in for Slack's send button, so nothing ever fires from mere typing.
  alt+r on a thread root re-sends it to the same session.
- Thread context = ancestor chain (for orientation) + the thread node's
  subtree, depth-first, mirrors expanded at most once (visited-set guard
  against mirror cycles).
- Replies stream back as events and land as `agent` child nodes (red ✦, chips
  allowed, read-only inline).
- `Client` is an interface. `wsClient` speaks JSON over a websocket to a Pi
  service (`coder/websocket`, already a dependency). `mockClient` is the
  offline stand-in used by tests and demos: deterministic canned replies, and
  a real artifact-generation path.
- Asking the agent for a node type ("create an artifact …") makes the service
  reply with an `artifact` event: the JS is installed into the artifacts
  table, hot-loaded into the running registry, and confirmed with an agent
  node — the new type is available in `/type` immediately.
- The bridge lives inside `lflow node open`. Closing the editor pauses
  sessions; ids persist, so a later mention in the same thread resumes context
  server-side.

Agent config lives in `~/.config/lflow/agents.json`
(`[{"name":"Pi","url":"ws://127.0.0.1:7431","mock":true}]`); with no file, a
built-in mock **Pi** is registered so the feature works out of the box.

## Protocol (websocket JSON, one message per line)

```
→ {"op":"send","session":"<id|empty>","agent":"Pi","thread":[{"uuid":…,"depth":…,"name":…,"type":…}]}
← {"op":"session","id":"s_…"}                       // assigned on first send
← {"op":"message","text":"…"}                        // one agent node
← {"op":"artifact","key":"…","label":"…","source":"…"}  // install request
← {"op":"done"}  |  {"op":"error","text":"…"}
```
