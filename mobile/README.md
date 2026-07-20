# lflow mobile

A Workflowy-style client for the lflow daemon: React + TypeScript + Vite.
It is a plain web app — every platform is a wrapper around the same build:

- **Any browser / desktop (Linux, Windows, macOS)**: `lflow serve --http :7420`
  serves the app at that address; open it, or install it as a PWA.
- **Android**: Capacitor wraps the same build into an APK (below).
- The Go binary embeds the built app (`pkg/tui/daemon/webui/dist`), so a plain
  `go build` needs no npm toolchain — dist is committed as a build artifact.

## Server

The daemon is the server (see `pkg/tui/daemon/http.go`):

    lflow serve --http :7420                  # LAN-visible API + app
    lflow serve --http 127.0.0.1:7420         # this machine only
    lflow serve --http :7420 --http-token S3C # require a bearer token

Endpoints: `GET /api/outline` (whole live tree), `POST /api/nodes`,
`PATCH|DELETE /api/nodes/{uuid}`, `POST /api/nodes/{uuid}/move`,
`GET /api/search?q=`, `GET /api/events` (SSE stream of committed changes —
the same events the TUI's live sync rides, so edits appear everywhere live).
Writes carry an `X-Lflow-Instance` header for echo suppression.

There is no auth by default — the outline is local-first; bind to localhost
or set `--http-token` when exposing it to a LAN.

## Development

    cd mobile
    npm install
    npm run dev        # Vite on :5173, /api proxied to :7420

Run `lflow serve --http :7420 --db <some-test-db> --sock <dir>/daemon.sock`
against a throwaway database, never the real outline.

`npm run build` typechecks and rebuilds the embedded dist; rebuild the Go
binary afterwards to re-embed it.

## Android (Capacitor)

Requires Android Studio / SDK on the machine doing the build:

    cd mobile
    npm install
    npm run build
    npx cap add android      # once; generates android/
    npm run android:sync     # copy web build into the native project
    npm run android:open     # open in Android Studio → run/build APK

The APK ships the web app; point Settings → Server URL at the machine
running `lflow serve --http` (e.g. `http://192.168.1.10:7420`).

## Custom note extensions

Node types are a registry (`src/extensions/registry.ts`), mirroring the TUI's
`registry.go`: builtins register at startup, and **custom extensions** are ES
modules that default-export

    {
      type: 'counter',        // free type string, stored in nodes.type
      label: 'Counter',       // type picker label
      glyph: '#',             // picker glyph (unicode, no emoji)
      inlineEditable: false,  // extension owns all interaction
      render(host) { ... }    // draws into host.el (plain DOM, no framework)
    }

`host` is the SDK surface: `host.el` (the row's DOM slot), `host.node` (the
node row), `host.update({name, note})` (persist through the server),
`host.children()`. Paste a module into Settings → Custom note extensions (see
`extension-examples/counter.js`) and the type appears in the picker as a
first-class node type — no rebuild, no schema change. The server accepts any
`[a-z0-9_-]{1,32}` type string; other clients render unknown types as plain
bullets, text intact.
