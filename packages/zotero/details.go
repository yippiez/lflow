package zotero

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"github.com/lflow/lflow/packages/database"
)

// Everything that hangs off one library entry: its tags, its attachments, the
// annotations made inside those attachments, and its notes. This is what the
// editor's zotero node mirrors into the outline.
//
// Details are read for ONE entry at a time, from their own fresh snapshot. The
// library index (Load) stays cheap and whole-library; annotations across a real
// library are tens of thousands of rows nobody has asked for yet.

// Tag is one Zotero tag, with the color Zotero paints it (a "#rrggbb" hex, or
// "" for the majority of tags, which have none).
type Tag struct {
	Name  string
	Color string
	Auto  bool // added by a translator rather than by hand
}

// Attachment is a file attached to an entry — usually the PDF.
type Attachment struct {
	Key         string
	Title       string
	ContentType string
	Path        string // absolute path on this machine; "" for a link-only attachment
	URL         string // for a linked-URL attachment
	Annotations []Annotation
}

// Annotation is one mark made inside an attachment: a highlight and/or a
// comment, in Zotero's own reading order.
type Annotation struct {
	Key     string
	Kind    string // highlight | underline | note | image | ink | annotation
	Text    string // the highlighted text ("" for image/ink marks)
	Comment string // what you typed next to it
	Color   string // "#rrggbb" as Zotero stores it
	Page    string // the page label Zotero shows
	// ImagePath is the picture Zotero rendered for a mark that HAS no text — an
	// area crop or a pen drawing. "" when the mark is textual, or when Zotero
	// has not drawn it yet (the cache fills lazily, as the reader displays a
	// page). See annotationImagePath for how it is found.
	ImagePath string
}

// HasImage reports whether the mark is a picture rather than text.
func (a Annotation) HasImage() bool { return a.ImagePath != "" }

// Note is a Zotero note attached to an entry, flattened out of its HTML.
type Note struct {
	Key   string
	Title string
	Text  string
}

// Details is everything mirrored under one entry.
type Details struct {
	Item        Item
	Tags        []Tag
	Attachments []Attachment
	Notes       []Note
	Abstract    string
	Date        string // the human date as Zotero shows it, not just the year
}

// Details reads one entry's tags, attachments, annotations and notes. The entry
// itself comes from the loaded index, so a key the library does not know is an
// error rather than an empty mirror.
func (l *Library) Details(key string) (*Details, error) {
	if l == nil {
		return nil, errors.New("no Zotero library loaded")
	}
	item, ok := l.ByKey(key)
	if !ok {
		return nil, errors.Errorf("no Zotero entry with key %s", key)
	}

	db, cleanup, err := openLibrary(l.Path)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	id, err := itemID(db, key)
	if err != nil {
		return nil, err
	}
	d := &Details{Item: item}
	if d.Tags, err = readTags(db, id); err != nil {
		return nil, err
	}
	if d.Attachments, err = readAttachments(db, id, filepath.Dir(l.Path), item.GroupID); err != nil {
		return nil, err
	}
	if d.Notes, err = readNotes(db, id); err != nil {
		return nil, err
	}
	d.Abstract, d.Date = readAbstractDate(db, id)
	return d, nil
}

// itemID resolves a Zotero item key to its local row id — the join key every
// child table uses.
func itemID(db *database.DB, key string) (int64, error) {
	var id int64
	if err := db.QueryRow(`SELECT itemID FROM items WHERE key = ?`, key).Scan(&id); err != nil {
		return 0, errors.Wrapf(err, "looking up zotero item %s", key)
	}
	return id, nil
}

// readTags returns an entry's tags, colored ones first (Zotero shows them in
// that priority), then manual, then the translator's automatic ones.
func readTags(db *database.DB, id int64) ([]Tag, error) {
	colors := readTagColors(db)
	rows, err := db.Query(`
		SELECT t.name, it.type
		FROM itemTags it JOIN tags t ON t.tagID = it.tagID
		WHERE it.itemID = ?`, id)
	if err != nil {
		return nil, errors.Wrap(err, "querying zotero tags")
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var name string
		var typ int
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, errors.Wrap(err, "scanning zotero tag")
		}
		out = append(out, Tag{Name: name, Color: colors[name], Auto: typ == 1})
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating zotero tags")
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Color != "") != (b.Color != "") {
			return a.Color != ""
		}
		if a.Auto != b.Auto {
			return !a.Auto
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out, nil
}

// tagColor is one entry of Zotero's own tagColors setting.
type tagColor struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// readTagColors returns tag name → "#rrggbb" from Zotero's tagColors setting,
// which is a JSON array. A library with no colored tags has no row at all.
func readTagColors(db *database.DB) map[string]string {
	out := map[string]string{}
	rows, err := db.Query(`SELECT value FROM settings WHERE setting = 'tagColors'`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		var colors []tagColor
		if json.Unmarshal([]byte(raw), &colors) != nil {
			continue
		}
		for _, c := range colors {
			if c.Name != "" && c.Color != "" {
				out[c.Name] = c.Color
			}
		}
	}
	return out
}

// linkMode values from itemAttachments: the two "imported" modes keep the file
// inside the Zotero storage directory, a linked file lives wherever the user
// put it, and a linked URL has no file at all.
const (
	linkImportedFile = 0
	linkImportedURL  = 1
	linkLinkedFile   = 2
)

// readAttachments returns an entry's attachments with their annotations, in
// Zotero's own order. dataDir is the Zotero data directory — the root the
// "storage:" and "attachments:" path prefixes resolve against.
func readAttachments(db *database.DB, id int64, dataDir, groupID string) ([]Attachment, error) {
	rows, err := db.Query(`
		SELECT a.itemID, i.key, a.linkMode, IFNULL(a.contentType, ''), IFNULL(a.path, '')
		FROM itemAttachments a JOIN items i ON i.itemID = a.itemID
		WHERE a.parentItemID = ?
		  AND a.itemID NOT IN (SELECT itemID FROM deletedItems)
		ORDER BY a.itemID`, id)
	if err != nil {
		return nil, errors.Wrap(err, "querying zotero attachments")
	}
	defer rows.Close()

	var out []Attachment
	var ids []int64
	for rows.Next() {
		var aid int64
		var linkMode int
		var key, contentType, path string
		if err := rows.Scan(&aid, &key, &linkMode, &contentType, &path); err != nil {
			return nil, errors.Wrap(err, "scanning zotero attachment")
		}
		at := Attachment{Key: key, ContentType: contentType}
		at.Path = resolvePath(dataDir, key, linkMode, path)
		out = append(out, at)
		ids = append(ids, aid)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating zotero attachments")
	}
	if len(out) == 0 {
		return nil, nil
	}

	// an attachment's display name is a title field like any other item's
	titles, err := attachmentTitles(db, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Title = firstNonEmpty(titles[ids[i]], filepath.Base(out[i].Path), out[i].Key)
		if out[i].Annotations, err = readAnnotations(db, ids[i], dataDir, groupID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// resolvePath turns Zotero's stored attachment path into an absolute path on
// this machine, or "" when the attachment is a bare link. An imported file
// lives under <dataDir>/storage/<attachment key>/; "attachments:" is the
// linked-file base directory; anything else is already a path.
func resolvePath(dataDir, key string, linkMode int, path string) string {
	switch {
	case path == "":
		return ""
	case strings.HasPrefix(path, "storage:"):
		return filepath.Join(dataDir, "storage", key, strings.TrimPrefix(path, "storage:"))
	case strings.HasPrefix(path, "attachments:"):
		return filepath.Join(dataDir, strings.TrimPrefix(path, "attachments:"))
	case linkMode == linkImportedFile || linkMode == linkImportedURL:
		return filepath.Join(dataDir, "storage", key, path)
	case linkMode == linkLinkedFile:
		return path
	}
	return path
}

// attachmentTitles reads the title field for a set of attachment rows.
func attachmentTitles(db *database.DB, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT d.itemID, v.value
		FROM itemData d
		JOIN fields f ON f.fieldID = d.fieldID
		JOIN itemDataValues v ON v.valueID = d.valueID
		WHERE f.fieldName = 'title' AND d.itemID IN (?` + strings.Repeat(", ?", len(ids)-1) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, errors.Wrap(err, "querying zotero attachment titles")
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, errors.Wrap(err, "scanning zotero attachment title")
		}
		out[id] = title
	}
	return out, errors.Wrap(rows.Err(), "iterating zotero attachment titles")
}

// annotationKinds maps Zotero's annotation type ids to a readable word. An id
// this table does not know still mirrors — as a generic "annotation" — because
// its text and comment are the part that matters.
var annotationKinds = map[int]string{
	1: "highlight",
	2: "note",
	3: "image",
	4: "ink",
	5: "underline",
}

// readAnnotations returns one attachment's annotations in reading order.
// itemAnnotations arrived with Zotero 6; an older library simply has no table,
// which reads as an entry with no annotations rather than an error.
func readAnnotations(db *database.DB, attachmentID int64, dataDir, groupID string) ([]Annotation, error) {
	rows, err := db.Query(`
		SELECT i.key, an.type, IFNULL(an.text, ''), IFNULL(an.comment, ''),
		       IFNULL(an.color, ''), IFNULL(an.pageLabel, ''), IFNULL(an.sortIndex, '')
		FROM itemAnnotations an JOIN items i ON i.itemID = an.itemID
		WHERE an.parentItemID = ?
		  AND an.itemID NOT IN (SELECT itemID FROM deletedItems)`, attachmentID)
	if err != nil {
		return nil, nil // pre-6 library: no annotations to mirror
	}
	defer rows.Close()

	type row struct {
		a    Annotation
		sort string
	}
	var all []row
	for rows.Next() {
		var r row
		var typ int
		if err := rows.Scan(&r.a.Key, &typ, &r.a.Text, &r.a.Comment, &r.a.Color, &r.a.Page, &r.sort); err != nil {
			return nil, errors.Wrap(err, "scanning zotero annotation")
		}
		r.a.Kind = firstNonEmpty(annotationKinds[typ], "annotation")
		r.a.Text = collapseSpace(r.a.Text)
		r.a.Comment = collapseSpace(r.a.Comment)
		if pictorialKinds[r.a.Kind] {
			r.a.ImagePath = annotationImagePath(dataDir, groupID, r.a.Key)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating zotero annotations")
	}
	// sortIndex is Zotero's own reading order: a zero-padded "page|offset|y"
	// string that sorts lexically.
	sort.SliceStable(all, func(i, j int) bool { return all[i].sort < all[j].sort })
	out := make([]Annotation, 0, len(all))
	for _, r := range all {
		out = append(out, r.a)
	}
	return out, nil
}

// pictorialKinds are the annotation kinds that ARE a picture: an area crop and
// a pen drawing carry no text, so their rendered image is the only thing to
// show. Zotero draws these lazily into its cache as the reader displays them.
var pictorialKinds = map[string]bool{"image": true, "ink": true}

// annotationImagePath locates the picture Zotero rendered for one mark, or ""
// when there is none.
//
// This cache is Zotero's own business and it is not a documented format: the
// known layout is <dataDir>/cache/library/<key>.png for the personal library
// and <dataDir>/cache/groups/<id>/<key>.png for a group. Those are tried first
// and then, because the layout is undocumented and has moved before, the cache
// directory is scanned shallowly for the key. Finding nothing is an ordinary
// outcome — the mark still mirrors, just without its picture.
func annotationImagePath(dataDir, groupID, key string) string {
	if dataDir == "" || key == "" {
		return ""
	}
	cache := filepath.Join(dataDir, "cache")
	candidates := []string{
		filepath.Join(cache, "library", key+".png"),
		filepath.Join(cache, "groups", groupID, key+".png"),
	}
	for _, p := range candidates {
		if groupID == "" && strings.Contains(p, "groups") {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return findInCache(cache, key+".png", 3)
}

// findInCache looks for a file name under root, at most depth levels down. The
// Zotero cache holds one small file per rendered annotation, so this stays a
// cheap walk over a flat-ish directory.
func findInCache(root, name string, depth int) string {
	if depth < 0 {
		return ""
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(root, e.Name()))
			continue
		}
		if e.Name() == name {
			return filepath.Join(root, e.Name())
		}
	}
	for _, d := range dirs {
		if hit := findInCache(d, name, depth-1); hit != "" {
			return hit
		}
	}
	return ""
}

// readNotes returns an entry's notes, HTML flattened to text.
func readNotes(db *database.DB, id int64) ([]Note, error) {
	rows, err := db.Query(`
		SELECT i.key, IFNULL(n.title, ''), IFNULL(n.note, '')
		FROM itemNotes n JOIN items i ON i.itemID = n.itemID
		WHERE n.parentItemID = ?
		  AND n.itemID NOT IN (SELECT itemID FROM deletedItems)
		ORDER BY n.itemID`, id)
	if err != nil {
		return nil, errors.Wrap(err, "querying zotero notes")
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		var html string
		if err := rows.Scan(&n.Key, &n.Title, &html); err != nil {
			return nil, errors.Wrap(err, "scanning zotero note")
		}
		n.Text = PlainText(html)
		n.Title = firstNonEmpty(collapseSpace(n.Title), clip(n.Text, 60))
		out = append(out, n)
	}
	return out, errors.Wrap(rows.Err(), "iterating zotero notes")
}

// readAbstractDate reads the two remaining fields the mirror shows: the
// abstract and the human-readable date. Both are optional.
func readAbstractDate(db *database.DB, id int64) (abstract, date string) {
	rows, err := db.Query(`
		SELECT f.fieldName, v.value
		FROM itemData d
		JOIN fields f ON f.fieldID = d.fieldID
		JOIN itemDataValues v ON v.valueID = d.valueID
		WHERE d.itemID = ? AND f.fieldName IN ('abstractNote', 'date')`, id)
	if err != nil {
		return "", ""
	}
	defer rows.Close()
	for rows.Next() {
		var field, value string
		if rows.Scan(&field, &value) != nil {
			continue
		}
		switch field {
		case "abstractNote":
			abstract = collapseSpace(value)
		case "date":
			date = humanDate(value)
		}
	}
	return abstract, date
}

// reMultipartDate splits Zotero's stored date: an SQL-ish part, then the string
// the user actually typed ("2017-06-12 2017-06-12", "0000-00-00 in press").
var reMultipartDate = regexp.MustCompile(`^\s*[0-9]{4}-[0-9]{2}-[0-9]{2}\s+(.*)$`)

// humanDate returns the date as Zotero displays it, dropping the machine half
// of the multipart value.
func humanDate(value string) string {
	if mm := reMultipartDate.FindStringSubmatch(value); mm != nil && strings.TrimSpace(mm[1]) != "" {
		return strings.TrimSpace(mm[1])
	}
	return strings.TrimSpace(value)
}

// reTag matches one HTML tag; reEntity one named or numeric character entity.
var (
	reTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	reEntity = regexp.MustCompile(`&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z]+);`)
)

// namedEntities are the handful worth decoding by name; anything else is left
// as typed rather than guessed at.
var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'", "nbsp": " ",
	"mdash": "—", "ndash": "–", "hellip": "…", "lsquo": "'", "rsquo": "'",
	"ldquo": `"`, "rdquo": `"`,
}

// PlainText flattens a Zotero note's HTML into outline text: block ends become
// spaces, tags are dropped, entities decoded. The outline stores no markup —
// keeping the HTML would break that invariant on the way in.
func PlainText(html string) string {
	if html == "" {
		return ""
	}
	s := regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|tr|blockquote)>|<br\s*/?>`).ReplaceAllString(html, " ")
	s = reTag.ReplaceAllString(s, "")
	s = reEntity.ReplaceAllStringFunc(s, func(e string) string {
		body := strings.Trim(e, "&;")
		if v, ok := namedEntities[strings.ToLower(body)]; ok {
			return v
		}
		if strings.HasPrefix(body, "#") {
			base, digits := 10, body[1:]
			if len(digits) > 0 && (digits[0] == 'x' || digits[0] == 'X') {
				base, digits = 16, digits[1:]
			}
			if n, err := strconv.ParseInt(digits, base, 32); err == nil && n > 0 {
				return string(rune(n))
			}
		}
		return e
	})
	return collapseSpace(s)
}

// collapseSpace trims and squeezes whitespace runs — outline text is one line.
func collapseSpace(s string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
}

// clip shortens a string to n runes with an ellipsis.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
