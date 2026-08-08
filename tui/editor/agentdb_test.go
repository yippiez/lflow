package editor

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// opencodeDBStore seeds a sandboxed XDG data dir with an opencode v1 SQLite
// store holding the seeded rows, and returns the database path.
func opencodeDBStore(t *testing.T, seed func(*sql.DB)) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := filepath.Join(home, ".local", "share")
	t.Setenv("XDG_DATA_HOME", data)
	dir := filepath.Join(data, "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		"CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT, directory TEXT, time_updated INTEGER, time_archived INTEGER)",
		"CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)",
		"CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT)",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if seed != nil {
		seed(db)
	}
	return path
}

func ocv1(t *testing.T) agentVariant {
	t.Helper()
	return variant(t, "opencode")
}

// TestAgentDBSessions lists the sessions of a v1 store for the picker: newest
// first, archived left out, and an untitled session named by its first prompt.
func TestAgentDBSessions(t *testing.T) {
	opencodeDBStore(t, func(db *sql.DB) {
		mustExec(t, db, "INSERT INTO session VALUES ('ses_old', 'older', '/a', 100, NULL)")
		mustExec(t, db, "INSERT INTO session VALUES ('ses_new', 'newer', '/b', 300, NULL)")
		mustExec(t, db, "INSERT INTO session VALUES ('ses_arch', 'archived', '/c', 200, 999)")
		mustExec(t, db, "INSERT INTO session VALUES ('ses_un', NULL, '/d', 250, NULL)")
		mustExec(t, db, "INSERT INTO message VALUES ('msg_u1', 'ses_un', 0, '{\"role\":\"user\"}')")
		mustExec(t, db, "INSERT INTO part VALUES ('p_u1', 'msg_u1', 'ses_un', 0, '{\"type\":\"text\",\"text\":\"rename the loop\"}')")
	})
	sessions := ocv1(t).agentSessions()
	if len(sessions) != 3 {
		t.Fatalf("listed %d sessions, want 3", len(sessions))
	}
	// newest first
	for i, want := range []string{"ses_new", "ses_un", "ses_old"} {
		if sessions[i].id != want {
			t.Fatalf("sessions[%d] = %q, want %q", i, sessions[i].id, want)
		}
	}
	if sessions[0].title != "newer" || sessions[0].cwd != "/b" {
		t.Errorf("newer = %+v", sessions[0])
	}
	// the untitled one is named by its first prompt
	if sessions[1].title != "rename the loop" {
		t.Errorf("untitled session title = %q, want the first prompt", sessions[1].title)
	}
	if sessions[1].variant != "opencode" {
		t.Errorf("variant = %q, want opencode", sessions[1].variant)
	}
	if sessions[0].updated.UnixMilli() != 300 {
		t.Errorf("updated = %v, want the store's clock", sessions[0].updated)
	}
}

// TestAgentDBLookupFindsArchived: a chip already pointing at a session reads it
// even when the session was archived out of the list.
func TestAgentDBLookupFindsArchived(t *testing.T) {
	opencodeDBStore(t, func(db *sql.DB) {
		mustExec(t, db, "INSERT INTO session VALUES ('ses_arch', 'archived', '/c', 200, 999)")
	})
	v := ocv1(t)
	if got := v.agentSessions(); len(got) != 0 {
		t.Fatalf("picker listed %d sessions, want none for an archived store", len(got))
	}
	s, ok := v.agentStoreSessionFor("ses_arch")
	if !ok || s.title != "archived" || s.cwd != "/c" {
		t.Errorf("lookup = %+v, %v", s, ok)
	}
}

// TestAgentDBOpenSkipsForeignSchema: a database without the v1 session table
// (opencode v2's layout) is not read as one it is not.
func TestAgentDBOpenSkipsForeignSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "next.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE session_message (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	v := agentVariant{dbPaths: func() []string { return []string{path} }}
	if got := v.openAgentDB(); got != nil {
		got.Close()
		t.Fatal("opened a store with no session table")
	}
}

// TestAgentDBPathsOnlyFiles: <data>/ paths resolve under XDG_DATA_HOME, and a
// directory never reads as a database.
func TestAgentDBPathsOnlyFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := filepath.Join(home, ".local", "share")
	t.Setenv("XDG_DATA_HOME", data)
	if err := os.MkdirAll(filepath.Join(data, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(data, "opencode", "opencode.db")
	if err := os.WriteFile(dbFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := agentDBPaths("<data>/opencode/opencode.db", "<data>/opencode/storage")
	if len(got) != 1 || got[0] != dbFile {
		t.Fatalf("paths = %v, want only the database file", got)
	}
}

// TestAgentDBOpenReadOnly: the CLI's store is opened read-only — a write must
// fail rather than touch opencode's own database.
func TestAgentDBOpenReadOnly(t *testing.T) {
	opencodeDBStore(t, nil)
	db := ocv1(t).openAgentDB()
	if db == nil {
		t.Fatal("store not opened")
	}
	defer db.Close()
	if _, err := db.db.Exec("INSERT INTO session VALUES ('x','x','x',1,NULL)"); err == nil {
		t.Fatal("read-only store accepted a write")
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
