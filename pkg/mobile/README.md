# lflow mobile

A cross-platform (iOS / Android / web) client for the lflow outline, built with
Expo + React Native + TypeScript. It connects to a running `lflow serve` over
WebSocket and renders the live outline 1:1 with the terminal editor.

## Architecture

- **Server** — `lflow serve` (Go, `pkg/tui/live`) owns the local SQLite outline,
  binds a LAN port (default `8765`) and serves clients over WebSocket with a
  shared-token. On connect a client gets a full **snapshot**; it streams edit
  **ops** back, which the server applies and re-broadcasts so all clients
  converge. See `pkg/tui/live/snapshot.go` and `ops.go`.
- **Client** — this app. The wire protocol lives in `src/protocol.ts` (kept in
  sync with the Go types). `src/connection.ts` manages the socket, persistence
  and reconnect; `src/theme.ts` ports the TUI "system" palette exactly.

## Run it

1. Start the server (on the machine holding your outline):

   ```sh
   lflow serve            # prints ws://<lan-ip>:8765/ws and a token
   ```

2. Start the app:

   ```sh
   cd pkg/mobile
   npm run web            # browser (fastest iteration)
   npm run ios            # iOS (needs a Mac) / Expo Go
   npm run android        # Android / Expo Go
   ```

3. In the app, enter the server `host:port` and paste the token.

The phone and the server must be on the same Wi-Fi (LAN mode). Tunnel support
(reach the server from anywhere) is a later add-on.

## Status

First build: live read + write — render the tree, expand/collapse, edit text,
add / delete nodes, toggle todos. The desktop TUI still opens the DB directly
for now; migrating it onto the server is a later phase.
