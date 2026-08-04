package zotero

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
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
CREATE TABLE itemAttachments (itemID INTEGER PRIMARY KEY, parentItemID INT, linkMode INT, contentType TEXT, path TEXT);
CREATE TABLE itemNotes (itemID INTEGER PRIMARY KEY, parentItemID INT, note TEXT, title TEXT);
CREATE TABLE itemAnnotations (itemID INTEGER PRIMARY KEY, parentItemID INT, type INT, authorName TEXT, text TEXT, comment TEXT, color TEXT, pageLabel TEXT, sortIndex TEXT, position TEXT);
CREATE TABLE tags (tagID INTEGER PRIMARY KEY, name TEXT UNIQUE);
CREATE TABLE itemTags (itemID INT, tagID INT, type INT, PRIMARY KEY (itemID, tagID));
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

	// the children of the journal article: a PDF with two annotations, a note,
	// and three tags — one of them colored in Zotero
	exec(`INSERT INTO items VALUES
		(20, 3, '2024-01-01 00:00:00', '2024-06-02 12:00:00', 1, 'PDF00001'),
		(21, 4, '2024-01-01 00:00:00', '2024-06-03 12:00:00', 1, 'NOTE0001'),
		(22, 3, '2024-01-01 00:00:00', '2024-06-04 12:00:00', 1, 'ANN00001'),
		(23, 3, '2024-01-01 00:00:00', '2024-06-05 12:00:00', 1, 'ANN00002'),
		(24, 3, '2024-01-01 00:00:00', '2024-06-06 12:00:00', 1, 'ANNTRASH')`)
	exec(`INSERT INTO itemAttachments VALUES (20, 10, 0, 'application/pdf', 'storage:paper.pdf')`)
	exec(`INSERT INTO itemDataValues VALUES (20, 'paper.pdf'), (21, 'The dominant models are recurrent.')`)
	exec(`INSERT INTO itemData VALUES (20, 1, 20)`)
	exec(`INSERT INTO itemNotes VALUES (21, 10, '<div><p>the residual stream framing &amp; why it works</p></div>', '')`)
	// deliberately inserted out of reading order: sortIndex is what orders them
	exec(`INSERT INTO itemAnnotations VALUES
		(23, 20, 1, '', 'the second highlight', '', '#ff6666', '9', '00002|000000|00100', ''),
		(22, 20, 1, '', 'the first highlight', 'my comment', '#ffd400', '3', '00001|000000|00100', ''),
		(24, 20, 1, '', 'a trashed highlight', '', '#5fb236', '4', '00001|000000|00200', '')`)
	exec(`INSERT INTO deletedItems VALUES (24, '2024-08-02 00:00:00')`)
	exec(`INSERT INTO tags VALUES (1, 'transformers'), (2, 'nlp'), (3, 'imported')`)
	exec(`INSERT INTO itemTags VALUES (10, 1, 0), (10, 2, 0), (10, 3, 1)`)
	exec(`INSERT INTO settings VALUES ('tagColors', '', '[{"name":"transformers","color":"#FFD400"}]')`)
	// the abstract, and a full date for the human-readable form
	exec(`INSERT INTO fields VALUES (7, 'abstractNote')`)
	exec(`INSERT INTO itemDataValues VALUES (30, 'We propose a new simple network architecture.')`)
	exec(`INSERT INTO itemData VALUES (10, 7, 30)`)
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

// mountRoot points the Windows-drive scan at a directory the test controls, so
// discovery never depends on the Zotero the developer's own machine has.
func mountRoot(t *testing.T, dir string) {
	t.Helper()
	prev := WindowsMountRoot
	WindowsMountRoot = func() string { return dir }
	t.Cleanup(func() { WindowsMountRoot = prev })
}

func TestDataDirPrefersTheEnvironment(t *testing.T) {
	mountRoot(t, t.TempDir())
	dir := t.TempDir()
	seedLibrary(t, dir)
	// Zotero's own env var is honored — the app itself was told where it lives
	t.Setenv("ZOTERO_DATA_DIR", dir)
	if got := DataDir(); got != dir {
		t.Errorf("DataDir = %q, want the ZOTERO_DATA_DIR override %q", got, dir)
	}
	// an override pointing nowhere falls through instead of winning
	t.Setenv("ZOTERO_DATA_DIR", filepath.Join(dir, "nope"))
	t.Setenv("HOME", t.TempDir())
	if got := DataDir(); got != "" {
		t.Errorf("DataDir = %q, want no library found without an override", got)
	}
	// Load with no directory uses the same discovery
	t.Setenv("ZOTERO_DATA_DIR", dir)
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
	mountRoot(t, t.TempDir())
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

// TestProfileDirsReadsZoterosOwnIndex covers the three things profiles.ini
// tells us that globbing a folder cannot: a profile kept outside the Zotero
// folder, which profile Zotero actually opens, and — under WSL — that an
// absolute path in there is a Windows one.
func TestProfileDirsReadsZoterosOwnIndex(t *testing.T) {
	mountRoot(t, "/mnt")
	root := t.TempDir()
	ini := "[General]\nStartWithLastProfile=1\n\n" +
		"[Profile0]\nName=other\nIsRelative=1\nPath=Profiles/aaa.other\n\n" +
		"[Profile1]\nName=default\nIsRelative=1\nPath=Profiles/bbb.default\nDefault=1\n\n" +
		"[Profile2]\nName=elsewhere\nIsRelative=0\nPath=" + elsewherePath() + "\n"
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	got := profileDirs(root)
	if len(got) < 3 {
		t.Fatalf("profileDirs = %v, want all three profiles", got)
	}
	// the default profile leads: it is the one Zotero opens
	if got[0] != filepath.Join(root, "Profiles", "bbb.default") {
		t.Errorf("profileDirs[0] = %q, want the Default=1 profile", got[0])
	}
	if !contains(got, filepath.Join(root, "Profiles", "aaa.other")) {
		t.Errorf("profileDirs = %v, missing the non-default profile", got)
	}
	if !contains(got, wantElsewhere()) {
		t.Errorf("profileDirs = %v, missing the absolute-path profile %q", got, wantElsewhere())
	}
}

// elsewherePath / wantElsewhere are the IsRelative=0 case: a Windows path when
// the test runs where one would appear, a plain absolute one otherwise.
func elsewherePath() string {
	if runtime.GOOS == "linux" {
		return `E:\Profiles\ccc.moved`
	}
	return filepath.Join(string(filepath.Separator), "profiles", "ccc.moved")
}

func wantElsewhere() string {
	if runtime.GOOS == "linux" {
		return "/mnt/e/Profiles/ccc.moved"
	}
	return elsewherePath()
}

// TestPrefsDataDirsFindsASandboxedZotero: a Flatpak Zotero keeps its profile
// under the sandbox home, not the real one.
func TestPrefsDataDirsFindsASandboxedZotero(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Flatpak and Snap are a Linux packaging")
	}
	mountRoot(t, t.TempDir())
	home := t.TempDir()
	profile := filepath.Join(home, ".var", "app", "org.zotero.Zotero", ".zotero", "zotero", "abc.default")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	prefs := "user_pref(\"extensions.zotero.dataDir\", \"/data/flatpak-zotero\");\n"
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"), []byte(prefs), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := prefsDataDirs(home); !contains(got, "/data/flatpak-zotero") {
		t.Errorf("prefsDataDirs = %v, want the Flatpak profile's dataDir", got)
	}
}

// TestCandidateDirsAreUnique: several profiles naming one library is normal,
// and the search list should not walk it repeatedly.
func TestCandidateDirsAreUnique(t *testing.T) {
	mountRoot(t, t.TempDir())
	home := t.TempDir()
	root := filepath.Join(home, ".zotero", "zotero")
	for _, name := range []string{"one.default", "two.other"} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		prefs := "user_pref(\"extensions.zotero.dataDir\", \"/data/shared\");\n"
		if err := os.WriteFile(filepath.Join(p, "prefs.js"), []byte(prefs), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("ZOTERO_DATA_DIR", "")
	seen := map[string]int{}
	for _, d := range candidateDirs() {
		seen[d]++
	}
	for d, n := range seen {
		if n > 1 {
			t.Errorf("candidateDirs lists %q %d times", d, n)
		}
	}
}

func contains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

func TestWSLPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("path translation only happens under WSL")
	}
	mountRoot(t, "/mnt")
	cases := map[string]string{
		// what a Windows Zotero writes into prefs.js, once unescaped
		`D:\Zotero`:             "/mnt/d/Zotero",
		`C:\Users\Eren\Zotero`:  "/mnt/c/Users/Eren/Zotero",
		"C:/Users/Eren/Zotero":  "/mnt/c/Users/Eren/Zotero",
		`d:\zotero`:             "/mnt/d/zotero",
		"/home/eren/Zotero":     "/home/eren/Zotero", // a native path is left alone
		"/mnt/d/Zotero":         "/mnt/d/Zotero",
		"":                      "",
		`\\server\share\Zotero`: `\\server\share\Zotero`, // no drive letter: not ours to rewrite
	}
	for in, want := range cases {
		if got := wslPath(in); got != want {
			t.Errorf("wslPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDataDirFindsAMovedWindowsLibrary is the WSL arrangement in full: Zotero
// runs on Windows with its library moved to D:\Zotero, lflow runs in WSL, and
// the only record of where the library went is the Windows profile's prefs.js.
func TestDataDirFindsAMovedWindowsLibrary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Windows-side scan is a WSL arrangement")
	}
	root := t.TempDir()
	mountRoot(t, root)
	t.Setenv("ZOTERO_DATA_DIR", "")
	t.Setenv("HOME", t.TempDir())

	// D:\Zotero, as WSL sees it
	moved := filepath.Join(root, "d", "Zotero")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	seedLibrary(t, moved)

	// C:\Users\Eren\AppData\Roaming\Zotero\Zotero\Profiles\r0wbbhka.default
	profile := filepath.Join(root, "c", "Users", "Eren", "AppData", "Roaming", "Zotero", "Zotero", "Profiles", "r0wbbhka.default")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	prefs := "user_pref(\"extensions.zotero.dataDir\", \"D:\\\\Zotero\");\n" +
		"user_pref(\"extensions.zotero.useDataDir\", true);\n"
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"), []byte(prefs), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DataDir(); got != moved {
		t.Fatalf("DataDir = %q, want the moved Windows library %q", got, moved)
	}
	// and it is genuinely readable from here, not just a path that parses
	lib, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Items) == 0 {
		t.Error("the Windows-side library loaded empty")
	}
}

// TestWindowsUserDirsSkipsSystemAccounts keeps the walk on real people.
func TestWindowsUserDirsSkipsSystemAccounts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Windows-side scan is a WSL arrangement")
	}
	root := t.TempDir()
	mountRoot(t, root)
	users := filepath.Join(root, "c", "Users")
	for _, name := range []string{"Eren", "Public", "Default", "Default User", "All Users"} {
		if err := os.MkdirAll(filepath.Join(users, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// desktop.ini sits beside them; a file is not an account
	if err := os.WriteFile(filepath.Join(users, "desktop.ini"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	got := windowsUserDirs()
	if len(got) != 1 || got[0] != filepath.Join(users, "Eren") {
		t.Errorf("windowsUserDirs = %v, want just the real account", got)
	}
	if dirs := windowsSideDirs(); len(dirs) != 1 || dirs[0] != filepath.Join(users, "Eren", "Zotero") {
		t.Errorf("windowsSideDirs = %v", dirs)
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

func TestDetails(t *testing.T) {
	dir := t.TempDir()
	seedLibrary(t, dir)
	lib, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := lib.Details("AAAA1111")
	if err != nil {
		t.Fatal(err)
	}

	if d.Item.Title != "Attention is all you need" {
		t.Errorf("details carry the wrong entry: %q", d.Item.Title)
	}
	if d.Abstract != "We propose a new simple network architecture." {
		t.Errorf("abstract = %q", d.Abstract)
	}
	// the human half of Zotero's multipart date, not the machine half
	if d.Date != "2017-06-12" {
		t.Errorf("date = %q, want the displayed form", d.Date)
	}

	// tags: colored first, then manual, then the translator's automatic ones
	var names []string
	for _, tag := range d.Tags {
		names = append(names, tag.Name)
	}
	if strings.Join(names, ",") != "transformers,nlp,imported" {
		t.Errorf("tags = %v, want colored → manual → automatic", names)
	}
	if d.Tags[0].Color != "#FFD400" {
		t.Errorf("tag color = %q, want Zotero's own", d.Tags[0].Color)
	}
	if d.Tags[0].Auto || !d.Tags[2].Auto {
		t.Errorf("automatic flags = %v / %v", d.Tags[0].Auto, d.Tags[2].Auto)
	}

	if len(d.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(d.Attachments))
	}
	at := d.Attachments[0]
	if at.Title != "paper.pdf" || at.ContentType != "application/pdf" {
		t.Errorf("attachment = %+v", at)
	}
	// an imported file resolves under <dataDir>/storage/<attachment key>/
	if want := filepath.Join(dir, "storage", "PDF00001", "paper.pdf"); at.Path != want {
		t.Errorf("attachment path = %q, want %q", at.Path, want)
	}

	// annotations come back in reading order, and the trashed one does not
	if len(at.Annotations) != 2 {
		t.Fatalf("annotations = %d, want the two live ones", len(at.Annotations))
	}
	if at.Annotations[0].Text != "the first highlight" {
		t.Errorf("annotations are not in reading order: %q first", at.Annotations[0].Text)
	}
	first := at.Annotations[0]
	if first.Kind != "highlight" || first.Comment != "my comment" || first.Color != "#ffd400" || first.Page != "3" {
		t.Errorf("annotation = %+v", first)
	}

	if len(d.Notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(d.Notes))
	}
	// the note's HTML is flattened — the outline stores no markup
	if got := d.Notes[0].Text; got != "the residual stream framing & why it works" {
		t.Errorf("note text = %q", got)
	}
}

func TestDetailsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	seedLibrary(t, dir)
	lib, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Details("NOSUCHKEY"); err == nil {
		t.Error("Details of an unknown key should fail")
	}
	var nilLib *Library
	if _, err := nilLib.Details("AAAA1111"); err == nil {
		t.Error("Details with no library should fail")
	}
}

func TestPlainText(t *testing.T) {
	cases := map[string]string{
		"<p>one</p><p>two</p>":      "one two",
		"a &amp; b &lt;c&gt;":       "a & b <c>",
		"<div>line<br/>break</div>": "line break",
		"&#8212; dash &#x2014; too": "— dash — too",
		"  <b>bold</b>   spaced  ":  "bold spaced",
		"":                          "",
		"&unknownentity; stays":     "&unknownentity; stays",
	}
	for in, want := range cases {
		if got := PlainText(in); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPDFURI(t *testing.T) {
	personal := Ref{Key: "PDF00001"}
	if got := personal.PDFURI(""); got != "zotero://open-pdf/library/items/PDF00001" {
		t.Errorf("PDFURI = %q", got)
	}
	if got := personal.PDFURI("ANN00001"); got != "zotero://open-pdf/library/items/PDF00001?annotation=ANN00001" {
		t.Errorf("PDFURI with annotation = %q", got)
	}
	group := Ref{Key: "PDF00001", GroupID: "5566"}
	if got := group.PDFURI(""); got != "zotero://open-pdf/groups/5566/items/PDF00001" {
		t.Errorf("group PDFURI = %q", got)
	}
}

func TestResolvePath(t *testing.T) {
	cases := []struct {
		path     string
		linkMode int
		want     string
	}{
		{"storage:paper.pdf", 0, "/zot/storage/KEY/paper.pdf"},
		{"attachments:sub/paper.pdf", 2, "/zot/sub/paper.pdf"},
		{"/elsewhere/paper.pdf", 2, "/elsewhere/paper.pdf"},
		{"paper.pdf", 0, "/zot/storage/KEY/paper.pdf"},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := resolvePath("/zot", "KEY", c.linkMode, c.path); got != c.want {
			t.Errorf("resolvePath(%q, mode %d) = %q, want %q", c.path, c.linkMode, got, c.want)
		}
	}
}

func TestHumanDate(t *testing.T) {
	cases := map[string]string{
		"2017-06-12 2017-06-12": "2017-06-12",
		"0000-00-00 June 2017":  "June 2017",
		"2020-01-00 2020-01":    "2020-01",
		"in press":              "in press",
		"":                      "",
	}
	for in, want := range cases {
		if got := humanDate(in); got != want {
			t.Errorf("humanDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnnotationImagePath(t *testing.T) {
	dir := t.TempDir()
	// the known layout: <dataDir>/cache/library/<key>.png
	lib := filepath.Join(dir, "cache", "library")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(lib, "ANN00001.png")
	if err := os.WriteFile(want, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := annotationImagePath(dir, "", "ANN00001"); got != want {
		t.Errorf("personal-library image = %q, want %q", got, want)
	}
	// a group library keeps its pictures under its own id
	grp := filepath.Join(dir, "cache", "groups", "5566")
	if err := os.MkdirAll(grp, 0o755); err != nil {
		t.Fatal(err)
	}
	gwant := filepath.Join(grp, "ANN00002.png")
	if err := os.WriteFile(gwant, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := annotationImagePath(dir, "5566", "ANN00002"); got != gwant {
		t.Errorf("group image = %q, want %q", got, gwant)
	}
	// the cache layout is Zotero's own and undocumented: a picture filed
	// somewhere else under cache/ is still found
	odd := filepath.Join(dir, "cache", "somewhere", "deeper")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	owant := filepath.Join(odd, "ANN00003.png")
	if err := os.WriteFile(owant, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := annotationImagePath(dir, "", "ANN00003"); got != owant {
		t.Errorf("relocated image = %q, want %q", got, owant)
	}
	// and a mark Zotero has not drawn yet simply has none
	if got := annotationImagePath(dir, "", "NOTDRAWN"); got != "" {
		t.Errorf("undrawn mark = %q, want none", got)
	}
	if got := annotationImagePath("", "", "ANN00001"); got != "" {
		t.Errorf("no data dir = %q", got)
	}
}

func TestDetailsFindsAnnotationImages(t *testing.T) {
	dir := t.TempDir()
	seedLibrary(t, dir)
	// make the second annotation an area crop, and cache a picture for it
	db, err := sql.Open("sqlite3", filepath.Join(dir, DBName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE itemAnnotations SET type = 3, text = '' WHERE itemID = 23`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	cache := filepath.Join(dir, "cache", "library")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "ANN00002.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	lib, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := lib.Details("AAAA1111")
	if err != nil {
		t.Fatal(err)
	}
	var crop, highlight *Annotation
	for i, a := range d.Attachments[0].Annotations {
		if a.Kind == "image" {
			crop = &d.Attachments[0].Annotations[i]
		} else {
			highlight = &d.Attachments[0].Annotations[i]
		}
	}
	if crop == nil || !crop.HasImage() {
		t.Fatalf("the area crop has no picture: %+v", crop)
	}
	if highlight == nil || highlight.HasImage() {
		t.Error("a textual highlight was given a picture path")
	}
}
