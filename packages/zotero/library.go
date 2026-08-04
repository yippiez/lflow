package zotero

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"github.com/lflow/lflow/packages/database"
)

// DBName is the Zotero library file inside a Zotero data directory.
const DBName = "zotero.sqlite"

// Library is a loaded snapshot of the local Zotero library: every citable
// entry, in memory, plus the account username the web-library URL needs. It is
// read once and searched in process — the picker filters on every keystroke,
// and re-reading a 100 MB SQLite file per keystroke is not a thing to do.
type Library struct {
	Path     string // the zotero.sqlite that was read
	Username string // zotero.org account name, from the library's own settings
	Items    []Item
	modTime  int64 // source mtime when loaded, so Stale() can tell
}

// ── locating the library ───────────────────────────────────────────────────

// DataDir finds the Zotero data directory the way Zotero itself would, so no
// user has to tell lflow anything. In order: Zotero's own ZOTERO_DATA_DIR, the
// dataDir recorded in any profile's prefs.js (set when the user moved their
// library), the default <home>/Zotero, and finally each Windows user's Zotero
// folder as mounted in WSL. Profiles come from Zotero's own profiles.ini (see
// profileDirs) across every install this machine can see (see appDirs) — which
// under WSL includes the Zotero on the Windows side, whose D:\… paths are
// translated to /mnt/d/…. Returns "" when no library file is found anywhere.
// The /settings "Zotero library" row shows the result (read-only); there is no
// lflow-specific env override — what lflow finds IS what Zotero itself uses,
// and a user with several libraries picks between them with ZOTERO_DATA_DIR,
// which is Zotero's own knob rather than one of ours.
func DataDir() string {
	for _, dir := range candidateDirs() {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, DBName)); err == nil {
			return dir
		}
	}
	return ""
}

// candidateDirs is DataDir's ordered search list, split out so the ordering is
// testable without a filesystem full of Zotero installs.
func candidateDirs() []string {
	var dirs []string
	dirs = append(dirs, os.Getenv("ZOTERO_DATA_DIR")) // Zotero's own env var, if the app was told
	home, _ := os.UserHomeDir()
	dirs = append(dirs, prefsDataDirs(home)...)
	if home != "" {
		// Zotero's default is <home>/Zotero — and inside a Flatpak or Snap
		// <home> is that packaging's sandbox home, not the real one
		dirs = append(dirs, filepath.Join(home, "Zotero"))
		for _, h := range sandboxHomes(home) {
			dirs = append(dirs, filepath.Join(h, "Zotero"))
		}
	}
	dirs = append(dirs, windowsSideDirs()...)
	return dedupe(dirs)
}

// dedupe drops repeats while holding the order — several profiles naming one
// data directory is the normal case, not an odd one.
func dedupe(dirs []string) []string {
	seen := make(map[string]bool, len(dirs))
	out := dirs[:0]
	for _, d := range dirs {
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// reDataDir pulls the data directory out of a Zotero prefs.js line:
//
//	user_pref("extensions.zotero.dataDir", "D:\\Research\\Zotero");
var reDataDir = regexp.MustCompile(`user_pref\("extensions\.zotero\.dataDir",\s*"((?:[^"\\]|\\.)*)"\)`)

// prefsDataDirs returns the data directories declared in every Zotero profile
// this machine can see — how a user who moved their library off the default
// path is still found.
func prefsDataDirs(home string) []string {
	var out []string
	for _, root := range appDirs(home) {
		for _, profile := range profileDirs(root) {
			b, err := os.ReadFile(filepath.Join(profile, "prefs.js"))
			if err != nil {
				continue
			}
			if mm := reDataDir.FindSubmatch(b); mm != nil {
				out = append(out, wslPath(unescapePref(string(mm[1]))))
			}
		}
	}
	return out
}

// appDirs lists the directories a Zotero install keeps its profile index in —
// one per install this machine can see: this platform's own, the Flatpak and
// Snap sandboxes, and — under WSL — every Windows account's, because the Zotero
// the user actually runs is the one on the Windows side and its prefs.js is the
// only place a moved library is written down.
func appDirs(home string) []string {
	var roots []string
	switch runtime.GOOS {
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			roots = append(roots, filepath.Join(appData, "Zotero", "Zotero"))
		}
	case "darwin":
		if home != "" {
			roots = append(roots, filepath.Join(home, "Library", "Application Support", "Zotero"))
		}
	default:
		if home != "" {
			roots = append(roots, filepath.Join(home, ".zotero", "zotero"))
			for _, h := range sandboxHomes(home) {
				roots = append(roots, filepath.Join(h, ".zotero", "zotero"))
			}
		}
	}
	for _, u := range windowsUserDirs() {
		roots = append(roots, filepath.Join(u, "AppData", "Roaming", "Zotero", "Zotero"))
	}
	return roots
}

// sandboxHomes are the homes a packaged Zotero sees instead of the real one.
// Both the profile index and the default data directory hang off these exactly
// as they do off a normal home, so one list answers for both.
func sandboxHomes(home string) []string {
	if runtime.GOOS != "linux" || home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".var", "app", "org.zotero.Zotero"), // Flatpak
		filepath.Join(home, "snap", "zotero", "common"),         // Snap
	}
}

// reINIKey splits an `IsRelative=1` line; Zotero's profiles.ini is the Firefox
// format it inherits.
var reINIKey = regexp.MustCompile(`^\s*(\w+)\s*=\s*(.*?)\s*$`)

// profileDirs lists an install's profile directories, best first. It reads
// Zotero's OWN index — profiles.ini —
//
//	[Profile0]
//	Path=Profiles/r0wbbhka.default
//	IsRelative=1
//	Default=1
//
// rather than guessing at the folder layout, which is what finds a profile the
// user keeps somewhere else entirely (IsRelative=0, an absolute path) and what
// says which profile Zotero actually opens (Default=1). The folders are then
// walked anyway, behind the index, for an install that has not written one yet.
func profileDirs(root string) []string {
	var def, rest []string
	if b, err := os.ReadFile(filepath.Join(root, "profiles.ini")); err == nil {
		var path string
		relative, isDefault := true, false
		flush := func() {
			if path == "" {
				return
			}
			p := wslPath(path)
			if relative {
				p = filepath.Join(root, filepath.FromSlash(path))
			}
			if isDefault {
				def = append(def, p)
			} else {
				rest = append(rest, p)
			}
			path, relative, isDefault = "", true, false
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "[") { // a new section
				flush()
				continue
			}
			m := reINIKey.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			switch m[1] {
			case "Path":
				path = m[2]
			case "IsRelative":
				relative = m[2] == "1"
			case "Default":
				isDefault = m[2] == "1"
			}
		}
		flush()
	}
	out := append(def, rest...)
	for _, dir := range []string{filepath.Join(root, "Profiles"), root} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	return dedupe(out)
}

// unescapePref undoes the backslash escaping prefs.js applies to Windows paths.
func unescapePref(s string) string {
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(s)
}

// windowsSideDirs lists the default Zotero data directories belonging to
// Windows user accounts as seen from a WSL mount — the "lflow in WSL, Zotero on
// the main PC" arrangement.
func windowsSideDirs() []string {
	users := windowsUserDirs()
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, filepath.Join(u, "Zotero"))
	}
	return out
}

// windowsUserDirs walks the mounted Windows drives for user profile folders
// (C:\Users\Eren as /mnt/c/Users/Eren). Empty on every other platform, and on a
// Linux box with no Windows drives to walk.
func windowsUserDirs() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	root := WindowsMountRoot()
	drives, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range drives {
		if len(d.Name()) != 1 { // drive letters only: /mnt/c, /mnt/d
			continue
		}
		usersDir := filepath.Join(root, d.Name(), "Users")
		users, err := os.ReadDir(usersDir)
		if err != nil {
			continue
		}
		for _, u := range users {
			switch u.Name() {
			case "Public", "Default", "Default User", "All Users":
				continue
			}
			if !u.IsDir() {
				continue
			}
			out = append(out, filepath.Join(usersDir, u.Name()))
		}
	}
	return out
}

// reWinPath matches an absolute Windows path by its drive letter: D:\Zotero,
// C:/Users/Eren/Zotero.
var reWinPath = regexp.MustCompile(`^([A-Za-z]):[\\/]*(.*)$`)

// wslPath rewrites a Windows path into the form WSL sees it under — D:\Zotero
// becomes /mnt/d/Zotero — so a dataDir read out of a Windows Zotero profile is
// something this process can actually open. A path that carries no drive letter
// comes back untouched, and nothing is rewritten off Linux.
func wslPath(p string) string {
	if runtime.GOOS != "linux" {
		return p
	}
	m := reWinPath.FindStringSubmatch(p)
	if m == nil {
		return p
	}
	return filepath.Join(WindowsMountRoot(), strings.ToLower(m[1]), strings.ReplaceAll(m[2], `\`, "/"))
}

// reAutomountRoot reads the [automount] root out of /etc/wsl.conf.
var reAutomountRoot = regexp.MustCompile(`(?m)^\s*root\s*=\s*(\S+)`)

// WindowsMountRoot answers where WSL mounts the Windows drives. It is a
// variable so tests — this package's and the editor's — can stand up a fake
// mount tree instead of depending on whatever Zotero the machine running them
// happens to have installed on its Windows side. Nothing in the app reassigns
// it.
var WindowsMountRoot = sync.OnceValue(detectWinMountRoot)

// detectWinMountRoot is /mnt unless the user moved it in /etc/wsl.conf.
func detectWinMountRoot() string {
	b, err := os.ReadFile("/etc/wsl.conf")
	if err != nil {
		return "/mnt"
	}
	if m := reAutomountRoot.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return "/mnt"
}

// ── reading the library ────────────────────────────────────────────────────

// Load reads the whole citable library out of the Zotero data directory. dir
// may be "" to search for one. A running Zotero holds its own locks on the
// file, so the read goes through a private snapshot copy (see snapshot) rather
// than the live database.
func Load(dir string) (*Library, error) {
	if dir == "" {
		dir = DataDir()
	}
	if dir == "" {
		return nil, errors.New("no Zotero library found — looked in ~/Zotero, your Zotero profiles' prefs.js, and the Windows side of this machine")
	}
	src := filepath.Join(dir, DBName)
	fi, err := os.Stat(src)
	if err != nil {
		return nil, errors.Wrapf(err, "reading %s", src)
	}

	db, cleanup, err := openLibrary(src)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	lib := &Library{Path: src, modTime: fi.ModTime().UnixNano()}
	if lib.Items, err = readItems(db); err != nil {
		return nil, err
	}
	lib.Username = readUsername(db)
	return lib, nil
}

// Stale reports whether Zotero has written to the library since it was loaded,
// so the caller can reload before showing the picker again.
func (l *Library) Stale() bool {
	if l == nil {
		return true
	}
	fi, err := os.Stat(l.Path)
	if err != nil {
		return false // the file went away: keep serving what we already read
	}
	return fi.ModTime().UnixNano() != l.modTime
}

// openLibrary returns a read-only handle on the library, and the cleanup that
// goes with it.
//
// It opens Zotero's OWN file, in SQLite's read-only mode. That is both the fast
// path and the correct one: the connection cannot write by construction, and
// SQLite's locking protocol is what guarantees the read sees a whole database
// rather than a half-finished write. It costs milliseconds.
//
// The fallback copies the file first (see snapshot) — for the setups where the
// live file cannot be opened at all, a library on a filesystem that will not
// grant the lock, or a Zotero left in WAL mode, whose -shm a read-only
// connection may not be allowed to map. A copy of a real library runs to
// hundreds of megabytes and, across a Windows mount, to a full second, so it is
// what happens when the fast path is refused, never the first thing tried.
func openLibrary(src string) (*database.DB, func(), error) {
	// through database.Open like every other sqlite handle in lflow — mode=ro is
	// a URI parameter, so the path travels as a file: URI
	if db, err := database.Open("file:" + src + "?mode=ro"); err == nil {
		// sql.Open is lazy — it has not touched the file yet. Reading the schema
		// is what actually takes the lock and proves this path works here.
		var one int
		if err = db.Conn.QueryRow("select 1 from sqlite_master limit 1").Scan(&one); err == nil {
			return db, func() { _ = db.Close() }, nil
		}
		_ = db.Close()
	}

	tmp, drop, err := snapshot(src)
	if err != nil {
		return nil, func() {}, err
	}
	db, err := database.Open(tmp)
	if err != nil {
		drop()
		return nil, func() {}, errors.Wrap(err, "opening zotero snapshot")
	}
	return db, func() { _ = db.Close(); drop() }, nil
}

// snapshot copies the library (and any write-ahead log beside it) into a
// throwaway directory and returns the copy's path — openLibrary's fallback,
// for a library the live read cannot open.
func snapshot(src string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "lflow-zotero-")
	if err != nil {
		return "", func() {}, errors.Wrap(err, "creating zotero snapshot dir")
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	dst := filepath.Join(dir, DBName)
	if err := copyFile(src, dst); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// the -wal/-shm siblings carry committed pages the main file does not have
	// yet; copied under the same basenames, SQLite recovers them into the copy.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = copyFile(src+suffix, dst+suffix)
	}
	return dst, cleanup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return errors.Wrapf(err, "opening %s", src)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return errors.Wrapf(err, "creating %s", dst)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return errors.Wrapf(err, "copying %s", src)
	}
	return errors.Wrapf(out.Close(), "closing %s", dst)
}

// citableItems selects every entry that can be cited: not in the trash, and not
// one of the container types (an attachment, a note, a PDF annotation) that
// hangs off a real entry rather than being one.
const citableItems = `
	SELECT i.itemID, i.key, i.libraryID, it.typeName, i.dateModified
	FROM items i
	JOIN itemTypes it ON it.itemTypeID = i.itemTypeID
	WHERE i.itemID NOT IN (SELECT itemID FROM deletedItems)
	  AND it.typeName NOT IN ('attachment', 'note', 'annotation')`

// readItems loads every citable entry with its fields and creators.
func readItems(db *database.DB) ([]Item, error) {
	groups, err := groupIDs(db)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(citableItems)
	if err != nil {
		return nil, errors.Wrap(err, "querying zotero items")
	}
	defer rows.Close()

	byID := map[int64]*Item{}
	var order []int64
	for rows.Next() {
		var id, libraryID int64
		var key, typeName, modified string
		if err := rows.Scan(&id, &key, &libraryID, &typeName, &modified); err != nil {
			return nil, errors.Wrap(err, "scanning zotero item")
		}
		byID[id] = &Item{Key: key, GroupID: groups[libraryID], Type: typeName, Modified: sqlTime(modified)}
		order = append(order, id)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating zotero items")
	}
	if err := readFields(db, byID); err != nil {
		return nil, err
	}
	if err := readCreators(db, byID); err != nil {
		return nil, err
	}

	out := make([]Item, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	// most recently touched first, so an empty query opens on what you are
	// actually working with
	sort.SliceStable(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out, nil
}

// itemFields is the set of Zotero fields a citation is built from. The title
// aliases (caseName, subject, nameOfAct) are how non-article item types spell
// their title.
var itemFields = []string{
	"title", "caseName", "subject", "nameOfAct",
	"date", "DOI", "url",
	"publicationTitle", "bookTitle", "proceedingsTitle",
}

// readFields folds itemData into the flat Item fields.
func readFields(db *database.DB, byID map[int64]*Item) error {
	q := `
		SELECT d.itemID, f.fieldName, v.value
		FROM itemData d
		JOIN fields f ON f.fieldID = d.fieldID
		JOIN itemDataValues v ON v.valueID = d.valueID
		WHERE f.fieldName IN (?` + strings.Repeat(", ?", len(itemFields)-1) + `)`
	args := make([]any, len(itemFields))
	for i, f := range itemFields {
		args[i] = f
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return errors.Wrap(err, "querying zotero item fields")
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var field, value string
		if err := rows.Scan(&id, &field, &value); err != nil {
			return errors.Wrap(err, "scanning zotero item field")
		}
		it, ok := byID[id]
		if !ok {
			continue // an attachment's or a trashed entry's field
		}
		switch field {
		case "title", "caseName", "subject", "nameOfAct":
			if it.Title == "" {
				it.Title = value
			}
		case "date":
			it.Year = yearOf(value)
		case "DOI":
			it.DOI = value
		case "url":
			it.URL = value
		case "publicationTitle", "bookTitle", "proceedingsTitle":
			if it.Journal == "" {
				it.Journal = value
			}
		}
	}
	return errors.Wrap(rows.Err(), "iterating zotero item fields")
}

// readCreators attaches author surnames in Zotero's own display order. A
// single-field creator (fieldMode 1 — an institution) carries its whole name in
// lastName, which is exactly what a citation wants.
func readCreators(db *database.DB, byID map[int64]*Item) error {
	rows, err := db.Query(`
		SELECT ic.itemID, c.lastName, c.firstName
		FROM itemCreators ic
		JOIN creators c ON c.creatorID = ic.creatorID
		ORDER BY ic.itemID, ic.orderIndex`)
	if err != nil {
		return errors.Wrap(err, "querying zotero creators")
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var last, first string
		if err := rows.Scan(&id, &last, &first); err != nil {
			return errors.Wrap(err, "scanning zotero creator")
		}
		it, ok := byID[id]
		if !ok {
			continue
		}
		name := strings.TrimSpace(last)
		if name == "" {
			name = strings.TrimSpace(first)
		}
		if name != "" {
			it.Creators = append(it.Creators, name)
		}
	}
	return errors.Wrap(rows.Err(), "iterating zotero creators")
}

// groupIDs maps a Zotero libraryID to its group id; the personal library is
// absent from the map (its entries carry no group).
func groupIDs(db *database.DB) (map[int64]string, error) {
	rows, err := db.Query(`SELECT libraryID, groupID FROM groups`)
	if err != nil {
		// a library that has never joined a group may predate the table
		return map[int64]string{}, nil
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var libraryID, groupID int64
		if err := rows.Scan(&libraryID, &groupID); err != nil {
			return nil, errors.Wrap(err, "scanning zotero group")
		}
		out[libraryID] = strconv.FormatInt(groupID, 10)
	}
	return out, errors.Wrap(rows.Err(), "iterating zotero groups")
}

// readUsername reads the signed-in zotero.org account name out of the library's
// own settings — so the cloud link works with no configuration at all when
// Zotero is synced. Values in that table are JSON-encoded strings.
func readUsername(db *database.DB) string {
	var raw string
	err := db.QueryRow(`SELECT value FROM settings WHERE setting = 'account' AND key = 'username'`).Scan(&raw)
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(raw), `"`)
}

// sqlTime parses Zotero's "YYYY-MM-DD HH:MM:SS" UTC timestamps into unix
// seconds; an unparseable value sorts oldest.
func sqlTime(s string) int64 {
	if len(s) < 10 {
		return 0
	}
	y, _ := strconv.Atoi(s[0:4])
	mo, _ := strconv.Atoi(s[5:7])
	d, _ := strconv.Atoi(s[8:10])
	var h, mi, sec int
	if len(s) >= 19 {
		h, _ = strconv.Atoi(s[11:13])
		mi, _ = strconv.Atoi(s[14:16])
		sec, _ = strconv.Atoi(s[17:19])
	}
	// a plain ordering key: the exact epoch does not matter, only the order
	return ((int64(y)*12+int64(mo))*31+int64(d))*86400 + int64(h)*3600 + int64(mi)*60 + int64(sec)
}

// ── searching ──────────────────────────────────────────────────────────────

// Search returns the entries matching a query, best first, capped at limit. The
// query is split on spaces and every term must appear somewhere in the entry
// ("smith attention 2017"), which is how you find a paper you only half
// remember. An empty query returns the most recently modified entries.
func (l *Library) Search(query string, limit int) []Item {
	if l == nil {
		return nil
	}
	terms := strings.Fields(strings.ToLower(query))
	var hits []Item
	for _, it := range l.Items {
		hay := it.searchText()
		matched := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				matched = false
				break
			}
		}
		if matched {
			hits = append(hits, it)
		}
	}
	if len(terms) > 0 {
		// an entry whose author or title OPENS with the first term is what the
		// user meant; everything else keeps the recency order behind it
		first := terms[0]
		sort.SliceStable(hits, func(i, j int) bool {
			return leads(hits[i], first) && !leads(hits[j], first)
		})
	}
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// ByKey returns the entry with a given item key.
func (l *Library) ByKey(key string) (Item, bool) {
	if l == nil {
		return Item{}, false
	}
	for _, it := range l.Items {
		if it.Key == key {
			return it, true
		}
	}
	return Item{}, false
}

// leads reports whether a term starts the entry's first author or its title —
// the strong-match test behind the search ranking.
func leads(it Item, term string) bool {
	if len(it.Creators) > 0 && strings.HasPrefix(strings.ToLower(it.Creators[0]), term) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(it.Title), term)
}
