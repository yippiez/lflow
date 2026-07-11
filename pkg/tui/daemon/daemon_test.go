package daemon_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/tui/client"
	"github.com/lflow/lflow/pkg/tui/daemon"
	"github.com/lflow/lflow/pkg/tui/database"
)

// startDaemon serves a fresh DB in a temp dir and returns its paths.
func startDaemon(t *testing.T) (dbPath, sock string) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "lflow.db")
	sock = filepath.Join(dir, "daemon.sock")

	store, err := daemon.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE nodes (
		uuid text PRIMARY KEY, parent_uuid text NOT NULL DEFAULT '', rank integer NOT NULL DEFAULT 0,
		name text NOT NULL DEFAULT '', note text NOT NULL DEFAULT '', type text NOT NULL DEFAULT 'bullets',
		style text NOT NULL DEFAULT '', mirror_of text NOT NULL DEFAULT '', completed_at integer NOT NULL DEFAULT 0,
		added_on integer NOT NULL DEFAULT 0, edited_on integer NOT NULL DEFAULT 0, deleted bool NOT NULL DEFAULT false,
		collapsed bool NOT NULL DEFAULT false, readonly bool NOT NULL DEFAULT false, starred bool NOT NULL DEFAULT false)`); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- daemon.Serve(store, daemon.Options{Sock: sock, Version: "test"})
	}()
	t.Cleanup(func() {
		// shutdown via a client so Serve returns cleanly
		if c, err := client.Ensure(dbPath, "test-stop", "test"); err == nil {
			c.Shutdown()
			c.Close()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		store.Close()
	})

	// wait for the socket
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := client.Ensure(dbPath, "probe", "test"); err == nil {
			c.Close()
			return dbPath, sock
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon did not come up")
	return "", ""
}

// TestRoundTrip drives the full stack: client driver → daemon → sqlite, with
// int64 timestamps surviving the wire, a transaction, and an event fanout.
func TestRoundTrip(t *testing.T) {
	dbPath, _ := startDaemon(t)

	a, err := client.Ensure(dbPath, "writer", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := client.Ensure(dbPath, "watcher", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	events, cancel, err := b.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	// int64 precision: UnixNano cannot survive a float64 round trip
	addedOn := int64(1752236423123456789)
	n := database.Node{UUID: "n1", Name: "hello", Type: "bullets", AddedOn: addedOn}
	if err := n.Insert(a.DB()); err != nil {
		t.Fatal(err)
	}

	got, err := database.GetNode(b.DB(), "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AddedOn != addedOn {
		t.Fatalf("AddedOn lost precision: %d != %d", got.AddedOn, addedOn)
	}
	if got.Name != "hello" {
		t.Fatalf("name = %q", got.Name)
	}

	// the watcher hears about the writer's insert, attributed to it
	select {
	case ev := <-events:
		if len(ev.Nodes) != 1 || ev.Nodes[0].UUID != "n1" {
			t.Fatalf("unexpected event: %+v", ev)
		}
		if ev.Instance != a.Instance() {
			t.Fatalf("event instance %q, want writer %q", ev.Instance, a.Instance())
		}
		if ev.Name != "writer" {
			t.Fatalf("event name %q", ev.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event for insert")
	}

	// a transaction: two writes, one event
	tx, err := a.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE nodes SET name = ? WHERE uuid = ?", "renamed", "n1"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO nodes (uuid, name) VALUES (?, ?)", "n2", "second"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-events:
		if len(ev.Nodes) != 2 {
			t.Fatalf("tx event carries %d nodes, want 2", len(ev.Nodes))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event for tx")
	}

	// rollback: no event
	tx2, err := a.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx2.Exec("UPDATE nodes SET name = 'x' WHERE uuid = 'n1'"); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		t.Fatalf("rollback produced an event: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}

	got, err = database.GetNode(a.DB(), "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" {
		t.Fatalf("after rollback name = %q, want renamed", got.Name)
	}
}

// TestConcurrentClients checks that a tx by one client blocks, not breaks, a
// concurrent writer, and everything lands.
func TestConcurrentClients(t *testing.T) {
	dbPath, _ := startDaemon(t)

	a, _ := client.Ensure(dbPath, "a", "test")
	defer a.Close()
	b, _ := client.Ensure(dbPath, "b", "test")
	defer b.Close()

	tx, err := a.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO nodes (uuid, name) VALUES ('t1', 'in-tx')"); err != nil {
		t.Fatal(err)
	}

	bDone := make(chan error, 1)
	go func() {
		_, err := b.DB().Exec("INSERT INTO nodes (uuid, name) VALUES ('t2', 'blocked')")
		bDone <- err
	}()

	select {
	case <-bDone:
		t.Fatal("b's write did not wait for a's open tx")
	case <-time.After(200 * time.Millisecond):
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-bDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("b's write never completed after commit")
	}

	var count int
	if err := a.DB().QueryRow("SELECT COUNT(*) FROM nodes").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}
