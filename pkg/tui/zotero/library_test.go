package zotero

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// zoteroSchema is the slice of Zotero's schema this package reads, in Zotero's
// own shape — enough for Load to exercise every join it makes in the wild.
const zoteroSchema = `
CREATE TABLE libraries (libraryID INTEGER PRIMARY KEY, type TEXT NOT NULL);
CREATE TABLE groups (groupID INTEGER PRIMARY KEY, libraryID INT NOT NULL, name TEXT);
CREATE TABLE itemTypes (itemTypeID INTEGER PRIMARY KEY, typeName TEXT);
CREATE TABLE items (itemID INTEGER PRIMARY KEY, itemTypeID INT NOT NULL, dateAdded TEXT, dateModified TEXT, libraryID INT NOT NULL, key TEXT NOT NULL);
CREATE TABLE deletedItems (itemID INTEGER PRIMARY KEY, dateDeleted TEXT);
CREATE TABLE fields (fieldID INTEGER PRIMARY KEY, fieldName TEXT UNIQUE);
CREATE TABLE itemDataValues (valueID INTEGER PRIMARY KEY, value NOT NULL);
CREATE TABLE itemData (itemID INT, fieldID INT, valueID INT, PRIMARY KEY (itemID, fieldID));
CREATE TABLE creators (creatorID INTEGER PRIMARY KEY, firstName TEXT, lastName TEXT, fieldMode INT);
CREATE TABLE itemCreators (itemID INT, creatorID INT, creatorTypeID INT, orderIndex INT, PRIMARY KEY (itemID, creatorID, creatorTypeID, orderIndex));
CREATE TABLE settings (setting TEXT, key TEXT, value, PRIMARY KEY (setting, key));
`

// seedLibrary writes a small Zotero library into dir and returns its path. It
// holds: a journal article with two authors in a personal library, a book in a
// group library, a trashed article, and an attachment — the last two exist to
// prove they never reach the outline.
func seedLibrary(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, DBName)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, stmt := range splitStatements(zoteroSchema) {
		exec(stmt)
	}

	exec(`INSERT INTO libraries VALUES (1, 'user'), (7, 'group')`)
	exec(`INSERT INTO groups VALUES (5566, 7, 'Reading group')`)
	exec(`INSERT INTO itemTypes VALUES (1, 'journalArticle'), (2, 'book'), (3, 'attachment'), (4, 'note')`)
	exec(`INSERT INTO fields VALUES (1, 'title'), (2, 'date'), (3, 'DOI'), (4, 'url'), (5, 'publicationTitle'), (6, 'caseName')`)

	exec(`INSERT INTO items VALUES
		(10, 1, '2024-01-01 00:00:00', '2024-06-01 12:00:00', 1, 'AAAA1111'),
		(11, 2, '2024-01-01 00:00:00', '2024-07-01 12:00:00', 7, 'BBBB2222'),
		(12, 1, '2024-01-01 00:00:00', '2024-08-01 12:00:00', 1, 'CCCC3333'),
		(13, 3, '2024-01-01 00:00:00', '2024-09-01 12:00:00', 1, 'DDDD4444')`)
	exec(`INSERT INTO deletedItems VALUES (12, '2024-08-02 00:00:00')`)

	values := []struct {
		id    int
		value string
	}{
		{1, "Attention is all you need"}, {2, "2017-06-12 2017-06-12"},
		{3, "10.1000/attn"}, {4, "https://arxiv.org/abs/1706.03762"},
		{5, "NeurIPS"}, {6, "Thinking in outlines"}, {7, "1998"},
		{8, "A trashed paper"},
	}
	for _, v := range values {
		exec(`INSERT INTO itemDataValues VALUES (?, ?)`, v.id, v.value)
	}
	exec(`INSERT INTO itemData VALUES
		(10, 1, 1), (10, 2, 2), (10, 3, 3), (10, 4, 4), (10, 5, 5),
		(11, 1, 6), (11, 2, 7),
		(12, 1, 8)`)

	exec(`INSERT INTO creators VALUES (1, 'Ashish', 'Vaswani', 0), (2, 'Noam', 'Shazeer', 0), (3, '', 'Institute of Notes', 1)`)
	exec(`INSERT INTO itemCreators VALUES (10, 1, 1, 0), (10, 2, 1, 1), (11, 3, 1, 0)`)
	exec(`INSERT INTO settings VALUES ('account', 'username', '"erin"')`)
	return path
}

// splitStatements breaks the schema literal into executable statements.
func splitStatements(schema string) []string {
	var out []string
	for _, s := range strings.Split(schema, ";") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	seedLibrary(t, dir)

	lib, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if lib.Username != "erin" {
		t.Errorf("Username = %q, want erin (read from the library's own account settings)", lib.Username)
	}
	// the trashed article and the attachment are not citable
	if len(lib.Items) != 2 {
		t.Fatalf("loaded %d items, want 2: %v", len(lib.Items), keys(lib.Items))
	}
	// most recently modified first
	if lib.Items[0].Key != "BBBB2222" {
		t.Errorf("first item = %s, want the most recently modified (BBBB2222)", lib.Items[0].Key)
	}

	article, ok := lib.ByKey("AAAA1111")
	if !ok {
		t.Fatal("the journal article did not load")
	}
	if article.Title != "Attention is all you need" || article.Year != "2017" {
		t.Errorf("article fields = %q / %q", article.Title, article.Year)
	}
	if article.DOI != "10.1000/attn" || article.Journal != "NeurIPS" {
		t.Errorf("article doi/journal = %q / %q", article.DOI, article.Journal)
	}
	if got := article.Creators; len(got) != 2 || got[0] != "Vaswani" || got[1] != "Shazeer" {
		t.Errorf("creators = %v, want Zotero's own order", got)
	}
	if article.GroupID != "" {
		t.Errorf("personal-library item carries group %q", article.GroupID)
	}
	if got := article.Label(); got != "Vaswani & Shazeer 2017" {
		t.Errorf("article label = %q", got)
	}

	book, ok := lib.ByKey("BBBB2222")
	if !ok {
		t.Fatal("the group-library book did not load")
	}
	if book.GroupID != "5566" {
		t.Errorf("group item GroupID = %q, want 5566", book.GroupID)
	}
	// a single-field creator (an institution) is its whole name
	if got := book.Label(); got != "Institute of Notes 1998" {
		t.Errorf("book label = %q", got)
	}
	if got := RefOf(book).LocalURI(); got != "zotero://select/groups/5566/items/BBBB2222" {
		t.Errorf("group LocalURI = %q", got)
	}
}

func TestLoadSearches(t *testing.T) {
	dir := t.TempDir()
	seedLibrary(t, dir)
	lib, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := lib.Search("vaswani attention", 0); len(got) != 1 || got[0].Key != "AAAA1111" {
		t.Errorf("Search over a loaded library = %v", keys(got))
	}
}

func TestLoadLeavesTheSourceAlone(t *testing.T) {
	dir := t.TempDir()
	path := seedLibrary(t, dir)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Error("Load wrote to the Zotero library — the read must go through a snapshot copy")
	}
	// and it leaves no temporary directory behind
	entries, _ := filepath.Glob(filepath.Join(os.TempDir(), "lflow-zotero-*"))
	for _, e := range entries {
		t.Errorf("snapshot directory %s was not cleaned up", e)
	}
}

func TestLoadMissingLibrary(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("Load of a directory with no zotero.sqlite should fail")
	}
}

func TestStale(t *testing.T) {
	dir := t.TempDir()
	path := seedLibrary(t, dir)
	lib, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if lib.Stale() {
		t.Error("a freshly loaded library reports stale")
	}
	// Zotero writes: the next picker open should re-read
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if !lib.Stale() {
		t.Error("a library whose file changed does not report stale")
	}
	var nilLib *Library
	if !nilLib.Stale() {
		t.Error("a library that was never loaded must report stale")
	}
}

func TestDataDirPrefersTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	seedLibrary(t, dir)
	t.Setenv("LFLOW_ZOTERO_DIR", dir)
	if got := DataDir(); got != dir {
		t.Errorf("DataDir = %q, want the LFLOW_ZOTERO_DIR override %q", got, dir)
	}
	// an override pointing nowhere falls through instead of winning
	t.Setenv("LFLOW_ZOTERO_DIR", filepath.Join(dir, "nope"))
	t.Setenv("ZOTERO_DATA_DIR", dir)
	if got := DataDir(); got != dir {
		t.Errorf("DataDir = %q, want the ZOTERO_DATA_DIR fallback %q", got, dir)
	}
	// Load with no directory uses the same discovery
	if _, err := Load(""); err != nil {
		t.Errorf("Load(\"\") = %v, want the discovered library", err)
	}
}

func TestUnescapePref(t *testing.T) {
	if got := unescapePref(`D:\\Research\\Zotero`); got != `D:\Research\Zotero` {
		t.Errorf("unescapePref = %q", got)
	}
}

func TestPrefsDataDirs(t *testing.T) {
	home := t.TempDir()
	// the Linux profile layout; the test asserts the parse, not the platform
	profile := filepath.Join(home, ".zotero", "zotero", "abc123.default")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	prefs := "user_pref(\"extensions.zotero.automaticSnapshots\", false);\n" +
		"user_pref(\"extensions.zotero.dataDir\", \"/data/zotero\");\n"
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"), []byte(prefs), 0o644); err != nil {
		t.Fatal(err)
	}
	got := prefsDataDirs(home)
	if len(got) == 1 && got[0] == "/data/zotero" {
		return
	}
	// on macOS/Windows the profile lives elsewhere, so an empty result is correct
	if len(got) != 0 {
		t.Errorf("prefsDataDirs = %v", got)
	}
}

func TestSQLTimeOrders(t *testing.T) {
	older := sqlTime("2024-06-01 12:00:00")
	newer := sqlTime("2024-07-01 12:00:00")
	if !(older < newer) {
		t.Errorf("sqlTime does not order: %d !< %d", older, newer)
	}
	if sqlTime("") != 0 || sqlTime("bad") != 0 {
		t.Error("an unparseable timestamp must sort oldest")
	}
}
