# Run Go tests via ./scripts/test.sh — the sqlite schema needs FTS5, so never run bare `go test ./...`; it must be `go test --tags fts5 ./...`.
