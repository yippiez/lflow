package editor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	// read the CLIs' own SQLite stores (opencode v1's opencode.db)
	_ "github.com/mattn/go-sqlite3"
)

// A CLI's sessions can live in a SQLite database rather than record files —
// opencode v1 keeps its whole store in opencode.db. lflow reads that database
// EXACTLY as it reads the record files: the picker lists the sessions, a chip
// looks one up, and the trace renders its parts. The database is opened
// READ-ONLY and nothing here ever writes to it — the CLI owns its store, and a
// note taker that edits it is a note taker you cannot trust with it.
//
// The reader is deliberately TOLERANT like the file readers: a database that
// is missing, locked, or of a layout this code does not know degrades to "no
// session found", never to an error.
//
// Only the v1 layout is read (a "session" table; content in "message" and
// "part" rows). opencode v2 keeps its store in opencode-next.db under a
// different schema, and a v2-only database is skipped, not misread.
type agentDB struct {
	path string // the database file — the store the sessions came from
	db   *sql.DB
}

// openAgentDB opens the variant's SQLite store read-only. Candidates are
// tried in order until one carries the v1 schema (a session table), which is
// also what keeps a v2 database from being read as one it is not.
func (v agentVariant) openAgentDB() *agentDB {
	if v.dbPaths == nil {
		return nil
	}
	for _, p := range v.dbPaths() {
		db, err := sql.Open("sqlite3", "file:"+p+"?mode=ro&_busy_timeout=5000")
		if err != nil {
			continue
		}
		if err := db.Ping(); err != nil {
			db.Close()
			continue
		}
		var one int
		if err := db.QueryRow(
			"SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'session'",
		).Scan(&one); err != nil {
			db.Close()
			continue
		}
		return &agentDB{path: p, db: db}
	}
	return nil
}

// Close releases the database. Every read opens its own connection and closes
// it, the way the file readers open and close a record per read.
func (db *agentDB) Close() error { return db.db.Close() }

// agentDBPaths resolves candidate SQLite store files under the user's data
// dir ("<data>/…" paths, XDG_DATA_HOME winning) or home, dropping the ones
// that do not exist. A file, not a directory — homeStores is for the record
// trees, this is for a database.
func agentDBPaths(paths ...string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	var out []string
	for _, p := range paths {
		abs := filepath.Join(home, p)
		if rest, ok := strings.CutPrefix(p, "<data>/"); ok {
			abs = filepath.Join(data, rest)
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			out = append(out, abs)
		}
	}
	return out
}

// sessions lists every session in the store, newest first, bounded like the
// record-file walk (agentScanCap). Archived sessions are left out the way the
// CLI's own list leaves them out; a chip already pointing at one still reads
// it through session.
func (db *agentDB) sessions() []agentStoreSession {
	rows, err := db.db.Query(`SELECT id, COALESCE(title, ''), COALESCE(directory, ''), COALESCE(time_updated, 0)
		FROM session WHERE time_archived IS NULL
		ORDER BY time_updated DESC LIMIT ?`, agentScanCap)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []agentStoreSession
	for rows.Next() {
		var s agentStoreSession
		var title, dir string
		var updated int64
		if err := rows.Scan(&s.id, &title, &dir, &updated); err != nil {
			continue
		}
		s.title = clipStr(oneLine(title), agentTitleCap)
		s.cwd = dir
		s.updated = time.UnixMilli(updated)
		if s.title == "" {
			// an untitled session is named by what was asked of it, the
			// same stand-in the file reader uses
			s.title = db.firstPrompt(s.id)
		}
		out = append(out, s)
	}
	return out
}

// session looks one session up — what a chip reads when the CLI renamed the
// session, or a picker row that came in without a name. Archived sessions are
// found: the chip already points at this one.
func (db *agentDB) session(id string) (agentStoreSession, bool) {
	var s agentStoreSession
	var title, dir string
	var updated int64
	err := db.db.QueryRow(`SELECT id, COALESCE(title, ''), COALESCE(directory, ''), COALESCE(time_updated, 0)
		FROM session WHERE id = ?`, id).Scan(&s.id, &title, &dir, &updated)
	if err != nil {
		return agentStoreSession{}, false
	}
	s.title = clipStr(oneLine(title), agentTitleCap)
	s.cwd = dir
	s.updated = time.UnixMilli(updated)
	if s.title == "" {
		s.title = db.firstPrompt(id)
	}
	return s, true
}

// firstPrompt is a session's opening user text — the fallback title for a
// session opencode never titled. The part scan is per session and bounded by
// how early a session's first user text sits, so it costs a small LIKE on the
// part rows of that one session.
func (db *agentDB) firstPrompt(id string) string {
	var data string
	err := db.db.QueryRow(`SELECT p.data FROM part p
		JOIN message m ON p.message_id = m.id
		WHERE p.session_id = ? AND m.data LIKE '%"role":"user"%'
		AND p.data LIKE '%"type":"text"%'
		ORDER BY p.time_created ASC LIMIT 1`, id).Scan(&data)
	if err != nil {
		return ""
	}
	var part map[string]any
	if json.Unmarshal([]byte(data), &part) != nil {
		return ""
	}
	text := agentPromptText(agentString(part, "text"))
	if text == "" {
		return ""
	}
	return clipStr(oneLine(text), agentTitleCap)
}
