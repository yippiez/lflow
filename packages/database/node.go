package database

import (
	"database/sql"
	"sort"
	"strings"

	"github.com/pkg/errors"
)

// Type values for a node.
const (
	TypeBullets = "bullets"
	TypeTodo    = "todo"
	TypeH1      = "h1"
	TypeH2      = "h2"
	TypeH3      = "h3"
	TypeCode    = "code"
	TypeQuote   = "quote"
	TypeLog     = "log" // a timestamped journal line: → glyph, dim time chip
	TypeJSON    = "json"
	// TypeBash is a shell command composed AS an outline (reinstated 2026-07-31,
	// after a spell where inline cmd chips were the only shell surface): a "$"
	// glyph row whose children are its arguments, and whose text may be a join
	// operator (| && || ;) or a wrapper ($() ()) that composes them — so a long
	// pipeline is written as a readable tree instead of one wide line. alt+r runs
	// the composed subtree. See editor/bashnode.go.
	TypeBash    = "bash"
	TypeQuery   = "query"
	TypeWeb     = "web" // a web-search node: the name is the query, alt+r searches SearxNG (see packages/integrations)
	TypeVoice   = "voice"
	TypeImage   = "image"
	TypeDivider = "divider"
	// TypeEmpty is a divider stripped of its rule: a deliberately blank row,
	// pure vertical breathing room in the outline. It carries no text.
	TypeEmpty = "empty"
	TypeWF    = "wf" // a Workflowy mirror root: alt+r pulls its subtree (see nodes/wf)
	// TypeNLPCompute is natural language as code: a red → instruction whose
	// alt+r generates the implementing snippet (see editor/nodes/nlpcompute.go).
	TypeNLPCompute = "nlpcompute"
	// TypeSVG and TypeHTML are markup composed AS an outline, the way TypeMath is
	// an expression composed as one: a node's text is a tag with its attributes
	// and its children are its child elements, so the outline structure IS the
	// document tree. alt+r serializes the subtree — an SVG node rasterizes it and
	// keeps the picture as its blob, an HTML node writes the markup to its run
	// band. See editor/markup.go.
	TypeSVG  = "svg"
	TypeHTML = "html"
	// TypePir is the home of the magic keywords: "ultracode" and "ultraloop"
	// animate on a Pir row and nowhere else, so the shine marks a deliberate
	// instruction rather than lighting up any note that happens to say the word.
	// Empty otherwise — the node's own behaviour is still to be written.
	TypePir = "pir"
	// TypeMath is a mathematical expression composed AS an outline: a node's text
	// is an operator (= ÷ ± √ Σ ∫ ^ …, colored yellow) with its operands as
	// children, or a plain atom leaf. Simple expressions stay inline on one row;
	// complex ones fan out into a child tree and the operator row shows a dim
	// linear preview of its whole subtree. See editor/math.go.
	TypeMath = "math"
	// TypePython is a Python statement composed AS an outline, the
	// programming-language sibling of TypeMath: a node's text is one logical
	// line — a compound-statement header (`if x > 3:`, `def foo(a):`, keywords
	// colored yellow) with its BODY as children, or a simple statement leaf.
	// The subtree renders back to real indented source (alt+r → run band), and
	// `lflow file open x.py` binds a whole file to such a tree. See
	// nodes/python.go.
	TypePython = "python"
	// TypeRust is the Rust sibling of TypePython: one logical line per node,
	// a `{`-opening header holds its block as children (braces regenerate
	// from structure on save). See nodes/rust.go.
	TypeRust = "rust"
	// The language-neutral construct types — first-class citizens shared by
	// every language codec, so a function is a function whether the file is
	// .py or .rs. Their text is the language-neutral core (a signature, the
	// comment body); each codec adds its own syntax back on save
	// (`def x():` / `fn x() {`, `# c` / `// c` / `<!-- c -->`). See
	// nodes/fn.go, nodes/class.go, nodes/comment.go, nodes/text.go.
	TypeFn      = "fn"      // a function: text is the signature, body is the children
	TypeClass   = "class"   // a type container (class / struct / impl / enum), members as children
	TypeComment = "comment" // a comment line, keyword-free text
	TypeText    = "text"    // a prose paragraph (markdown text that is NOT a list item)
	// TypeTable reads an ordinary subtree AS a grid: the table's first-level
	// children are its columns (their text is the header), each column's children
	// are that column's cells top to bottom (so row n is the nth child of every
	// column), and a cell's own children are the outline inside that cell. No node
	// moves when a node becomes a table — it is a reading, so any node converts and
	// the grid ⇄ nodes toggle is lossless. See editor/table.go.
	TypeTable = "table"
	// TypeThinking is a plain muted-gray marker node — Claude Code's thinking
	// glyph and nothing else (no special editor behavior).
	TypeThinking = "thinking"
	// TypeMol is a molecule composed AS an outline: one atom per node, children
	// are the atoms bonded to it; alt+e draws the structure. See editor/molecule.go.
	TypeMol  = "molecule"
	TypeLine = "line"
	// TypeAgent was a whole-row handle on an agentic coding session, beside the
	// inline chip. The node was removed 2026-08-04 — one session had two surfaces
	// and the row was the worse of them — but the key is kept VALID so a node
	// somebody still has typed that way keeps its own name instead of reading as
	// an unknown type. The editor no longer offers it, and the chip (chipKindAgent,
	// see editor/agent.go) is the session's one surface.
	TypeAgent = "agent"
	// TypeWebResult is one web-search hit: a generated link row a web node hangs
	// under it (title + URL link chip). It is generated, so it never appears in
	// the /type picker; a re-run replaces the rows by type. See editor/webnode.go
	// and packages/integrations.
	TypeWebResult = "webresult"

	// TypeZotero is a mirrored Zotero entry: one node per library item, its
	// attachments, annotations and notes pulled in beneath it as a locked
	// subtree (see editor/zoteroitem.go and packages/integrations).
	TypeZotero = "zotero"
)

// Priority values for a node: where incoming nodes land among its children.
// "up" places new/moved-in nodes at the top, "down" at the bottom. Existing
// rows were backfilled to down (lm39); nodes created since default to up.
// An empty value (pre-feature rows written by raw INSERTs) reads as down.
const (
	PriorityUp   = "up"
	PriorityDown = "down"
)

// TypeOrder is the canonical ordering of node types — the single source of
// truth for the accepted set. ValidTypes and the human-readable list in CLI
// help/errors derive from it, so a new type added here needs no other edits.
var TypeOrder = []string{
	TypeBullets,
	TypeTodo,
	TypeDivider,
	TypeEmpty,
	TypeH1,
	TypeH2,
	TypeH3,
	TypeCode,
	TypeQuote,
	TypeLog,
	TypeJSON,
	TypeBash,
	TypeQuery,
	TypeWeb,
	TypeVoice,
	TypeImage,
	TypeNLPCompute,
	TypeSVG,
	TypeHTML,
	TypePir,
	TypeMath,
	TypePython,
	TypeRust,
	TypeFn,
	TypeClass,
	TypeComment,
	TypeText,
	TypeTable,
	TypeMol,
	TypeWF,
	TypeThinking,
	TypeLine,
	TypeAgent,
	TypeWebResult,
	TypeZotero,
}

// ValidTypes is the set of accepted type values, derived from TypeOrder.
var ValidTypes = func() map[string]bool {
	m := make(map[string]bool, len(TypeOrder))
	for _, t := range TypeOrder {
		m[t] = true
	}
	return m
}()

// TypeList renders the accepted types as a human list, e.g.
// "bullets, todo, ... or voice", for flag help and error messages.
func TypeList() string {
	switch len(TypeOrder) {
	case 0:
		return ""
	case 1:
		return TypeOrder[0]
	}
	return strings.Join(TypeOrder[:len(TypeOrder)-1], ", ") + " or " + TypeOrder[len(TypeOrder)-1]
}

// LockMode is a bitset persisted in nodes.readonly. The old boolean values stay
// storage compatible: 0 is unlocked and 1 is the original content lock.
type LockMode uint8

const (
	LockReadWrite     LockMode = 1 << iota // text/note/type edits
	LockIndentOutdent                      // hierarchy and ordering changes
)

func (l LockMode) Has(flag LockMode) bool { return l&flag != 0 }

// Node is the single content model: every bullet, heading, todo and mirror
// instance is a node. ParentUUID == "" means a root in the local forest.
//
// WARNING (invariant): no markup leaks into stored text. Name holds PLAIN text
// only — styling and dates are per-node attributes (Style, MirrorOf,
// CompletedAt) or render-time chips, never inline markers baked into Name.
type Node struct {
	RowID       int    `json:"rowid"`
	UUID        string `json:"uuid"`
	ParentUUID  string `json:"parent_uuid"`
	Rank        int    `json:"rank"`
	Name        string `json:"name"`
	Note        string `json:"note"`
	Type        string `json:"type"`
	Style       string `json:"style"` // comma-separated style tokens, e.g. "bold,color:blue"
	MirrorOf    string `json:"mirror_of"`
	CompletedAt int64  `json:"completed_at"` // 0 = not completed
	AddedOn     int64  `json:"added_on"`
	EditedOn    int64  `json:"edited_on"`
	Deleted     bool   `json:"deleted"`
	Collapsed   bool   `json:"collapsed"` // local view-state
	// Readonly is the compatibility view of LockReadWrite. Lock carries the full
	// union so either lock, or both, can be active independently.
	Readonly bool     `json:"readonly"`
	Lock     LockMode `json:"lock,omitempty"`
	Starred  bool     `json:"starred"`  // /star: pinned to the top of pickers and search hits
	Priority string   `json:"priority"` // /priority: where incoming nodes land — "up" = top, "down"/"" = bottom
}

const nodeColumns = "uuid, parent_uuid, rank, name, note, type, style, mirror_of, completed_at, added_on, edited_on, deleted, collapsed, readonly, starred, priority"

func scanNode(row interface{ Scan(...interface{}) error }) (Node, error) {
	var n Node
	var lock int64
	err := row.Scan(&n.UUID, &n.ParentUUID, &n.Rank, &n.Name, &n.Note, &n.Type,
		&n.Style, &n.MirrorOf, &n.CompletedAt, &n.AddedOn, &n.EditedOn, &n.Deleted, &n.Collapsed, &lock, &n.Starred, &n.Priority)
	n.Lock = LockMode(lock)
	n.Readonly = n.Lock.Has(LockReadWrite)
	return n, err
}

// LockValue returns the union stored in the legacy readonly integer column.
// Older callers often set only Readonly, so fold that compatibility field in.
func (n Node) LockValue() LockMode {
	l := n.Lock
	if n.Readonly {
		l |= LockReadWrite
	}
	return l
}

// Insert inserts the node.
func (n Node) Insert(db *DB) error {
	_, err := db.Exec("INSERT INTO nodes ("+nodeColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		n.UUID, n.ParentUUID, n.Rank, n.Name, n.Note, n.Type, n.Style, n.MirrorOf, n.CompletedAt,
		n.AddedOn, n.EditedOn, n.Deleted, n.Collapsed, n.LockValue(), n.Starred, n.Priority)
	if err != nil {
		return errors.Wrapf(err, "inserting node %s", n.UUID)
	}
	return nil
}

// Upsert inserts the node, or — if a row with the same uuid already exists — revives
// and overwrites it (deleted = 0). A delete that was saved (tombstoning the row and
// dropping its in-memory snapshot) and then undone leaves the editor believing the
// restored node is brand new; a plain INSERT would then crash on UNIQUE(uuid). The
// original added_on is preserved. FTS stays consistent via the AFTER UPDATE
// trigger (which fires on the conflict branch).
func (n Node) Upsert(db *DB) error {
	_, err := db.Exec("INSERT INTO nodes ("+nodeColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) "+
		`ON CONFLICT(uuid) DO UPDATE SET
			parent_uuid = excluded.parent_uuid, rank = excluded.rank, name = excluded.name,
			note = excluded.note, type = excluded.type, style = excluded.style,
			mirror_of = excluded.mirror_of, completed_at = excluded.completed_at,
			edited_on = excluded.edited_on, collapsed = excluded.collapsed,
			readonly = excluded.readonly, starred = excluded.starred,
			priority = excluded.priority, deleted = 0`,
		n.UUID, n.ParentUUID, n.Rank, n.Name, n.Note, n.Type, n.Style, n.MirrorOf, n.CompletedAt,
		n.AddedOn, n.EditedOn, n.Deleted, n.Collapsed, n.LockValue(), n.Starred, n.Priority)
	if err != nil {
		return errors.Wrapf(err, "upserting node %s", n.UUID)
	}
	return nil
}

// SetCollapsed persists a node's collapsed flag. It is local view-state, so it
// leaves edited_on untouched.
func SetCollapsed(db *DB, uuid string, collapsed bool) error {
	if _, err := db.Exec("UPDATE nodes SET collapsed = ? WHERE uuid = ?", collapsed, uuid); err != nil {
		return errors.Wrapf(err, "setting collapsed for %s", uuid)
	}
	return nil
}

// SetStarred persists a node's /star flag. Like collapse it is toggled in
// place, so it writes immediately and leaves edited_on untouched.
func SetStarred(db *DB, uuid string, starred bool) error {
	if _, err := db.Exec("UPDATE nodes SET starred = ? WHERE uuid = ?", starred, uuid); err != nil {
		return errors.Wrapf(err, "setting starred for %s", uuid)
	}
	return nil
}

// SetPriority persists a node's /priority preference. Like collapse and star it
// is toggled in place, so it writes immediately and leaves edited_on untouched.
func SetPriority(db *DB, uuid, priority string) error {
	if _, err := db.Exec("UPDATE nodes SET priority = ? WHERE uuid = ?", priority, uuid); err != nil {
		return errors.Wrapf(err, "setting priority for %s", uuid)
	}
	return nil
}

// SetAddedOn rewrites a node's creation date (the /reborn command). Like
// star and priority it writes in place and leaves edited_on untouched.
func SetAddedOn(db *DB, uuid string, addedOn int64) error {
	if _, err := db.Exec("UPDATE nodes SET added_on = ? WHERE uuid = ?", addedOn, uuid); err != nil {
		return errors.Wrapf(err, "setting added_on for %s", uuid)
	}
	return nil
}

// Update persists all mutable fields of the node.
func (n Node) Update(db *DB) error {
	_, err := db.Exec(`UPDATE nodes SET parent_uuid = ?, rank = ?, name = ?, note = ?, type = ?,
		style = ?, mirror_of = ?, completed_at = ?, edited_on = ?, deleted = ?, readonly = ? WHERE uuid = ?`,
		n.ParentUUID, n.Rank, n.Name, n.Note, n.Type, n.Style, n.MirrorOf, n.CompletedAt,
		n.EditedOn, n.Deleted, n.LockValue(), n.UUID)
	if err != nil {
		return errors.Wrapf(err, "updating node %s", n.UUID)
	}
	return nil
}

// UpdateUUID rewrites the node's uuid and every reference to it
// (children's parent_uuid, mirrors' mirror_of).
func (n *Node) UpdateUUID(db *DB, newUUID string) error {
	if _, err := db.Exec("UPDATE nodes SET uuid = ? WHERE uuid = ?", newUUID, n.UUID); err != nil {
		return errors.Wrapf(err, "updating node uuid %s -> %s", n.UUID, newUUID)
	}
	if _, err := db.Exec("UPDATE nodes SET parent_uuid = ? WHERE parent_uuid = ?", newUUID, n.UUID); err != nil {
		return errors.Wrapf(err, "reparenting children of %s", n.UUID)
	}
	if _, err := db.Exec("UPDATE nodes SET mirror_of = ? WHERE mirror_of = ?", newUUID, n.UUID); err != nil {
		return errors.Wrapf(err, "updating mirrors of %s", n.UUID)
	}
	n.UUID = newUUID
	return nil
}

// Expunge hard-deletes the node row.
func (n Node) Expunge(db *DB) error {
	if _, err := db.Exec("DELETE FROM nodes WHERE uuid = ?", n.UUID); err != nil {
		return errors.Wrapf(err, "expunging node %s", n.UUID)
	}
	return nil
}

// GetNode returns the node with the given uuid.
func GetNode(db *DB, uuid string) (Node, error) {
	n, err := scanNode(db.QueryRow("SELECT "+nodeColumns+" FROM nodes WHERE uuid = ?", uuid))
	if err == sql.ErrNoRows {
		return n, errors.Errorf("node %s not found", uuid)
	} else if err != nil {
		return n, errors.Wrapf(err, "querying node %s", uuid)
	}
	return n, nil
}

// GetChildren returns the non-deleted children of the given parent ("" = roots),
// ordered by rank.
func GetChildren(db *DB, parentUUID string) ([]Node, error) {
	rows, err := db.Query("SELECT "+nodeColumns+" FROM nodes WHERE parent_uuid = ? AND deleted = 0 ORDER BY rank", parentUUID)
	if err != nil {
		return nil, errors.Wrap(err, "querying children")
	}
	defer rows.Close()

	var ret []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, errors.Wrap(err, "scanning node")
		}
		ret = append(ret, n)
	}
	return ret, rows.Err()
}

// GetSubtree returns the node and all of its non-deleted descendants,
// depth-first, siblings ordered by rank.
func GetSubtree(db *DB, rootUUID string) ([]Node, error) {
	root, err := GetNode(db, rootUUID)
	if err != nil {
		return nil, err
	}

	ret := []Node{root}
	var walk func(parent string) error
	walk = func(parent string) error {
		children, err := GetChildren(db, parent)
		if err != nil {
			return err
		}
		for _, c := range children {
			ret = append(ret, c)
			if err := walk(c.UUID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(rootUUID); err != nil {
		return nil, err
	}
	return ret, nil
}

// GetNodesWhere returns nodes matching an arbitrary WHERE condition — the
// daemon uses it to fetch fresh rows for the rowids an update hook collected.
func GetNodesWhere(db *DB, cond string, args ...interface{}) ([]Node, error) {
	rows, err := db.Query("SELECT "+nodeColumns+" FROM nodes WHERE "+cond, args...)
	if err != nil {
		return nil, errors.Wrap(err, "querying nodes")
	}
	defer rows.Close()

	var ret []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, errors.Wrap(err, "scanning node")
		}
		ret = append(ret, n)
	}
	return ret, rows.Err()
}

// NextRank returns a rank that sorts after all existing children of the parent.
func NextRank(db *DB, parentUUID string) (int, error) {
	var maxRank sql.NullInt64
	if err := db.QueryRow("SELECT MAX(rank) FROM nodes WHERE parent_uuid = ? AND deleted = 0", parentUUID).Scan(&maxRank); err != nil {
		return 0, errors.Wrap(err, "querying max rank")
	}
	if !maxRank.Valid {
		return 0, nil
	}
	return int(maxRank.Int64) + 1, nil
}

// FirstRank returns a rank that sorts before all existing children of the parent,
// so a node moved in lands at the top of the list rather than the bottom. It is
// PlaceRank's "up" half, and what an explicit position: "top" asks for.
func FirstRank(db *DB, parentUUID string) (int, error) {
	var minRank sql.NullInt64
	if err := db.QueryRow("SELECT MIN(rank) FROM nodes WHERE parent_uuid = ? AND deleted = 0", parentUUID).Scan(&minRank); err != nil {
		return 0, errors.Wrap(err, "querying min rank")
	}
	if !minRank.Valid {
		return 0, nil
	}
	return int(minRank.Int64) - 1, nil
}

// PlaceRank returns the rank for a node entering the parent with no explicit
// position, honoring the parent's priority: up → above the existing children,
// down (or unset, or no such parent row) → below them.
func PlaceRank(db *DB, parentUUID string) (int, error) {
	var prio string
	err := db.QueryRow("SELECT priority FROM nodes WHERE uuid = ?", parentUUID).Scan(&prio)
	if err != nil && err != sql.ErrNoRows {
		return 0, errors.Wrapf(err, "querying priority of %s", parentUUID)
	}
	if prio == PriorityUp {
		return FirstRank(db, parentUUID)
	}
	return NextRank(db, parentUUID)
}

// Reparent moves a node under a new parent at the given rank. Like a
// collapse/star toggle it is a structural change, so it leaves edited_on
// untouched — the node's content did not change, only its position. Callers
// that want the move to count as a recent edit use ReparentTouched.
func Reparent(db *DB, uuid, parentUUID string, rank int) error {
	if _, err := db.Exec("UPDATE nodes SET parent_uuid = ?, rank = ? WHERE uuid = ?",
		parentUUID, rank, uuid); err != nil {
		return errors.Wrapf(err, "reparenting node %s", uuid)
	}
	return nil
}

// ReparentTouched moves a node like Reparent but also stamps edited_on, so the
// move surfaces the node as recently touched (used by the `lflow mv` CLI).
func ReparentTouched(db *DB, uuid, parentUUID string, rank int, editedOn int64) error {
	if _, err := db.Exec("UPDATE nodes SET parent_uuid = ?, rank = ?, edited_on = ? WHERE uuid = ?",
		parentUUID, rank, editedOn, uuid); err != nil {
		return errors.Wrapf(err, "reparenting node %s", uuid)
	}
	return nil
}

// ShiftRanksAll adds delta to the rank of every non-deleted child of the parent,
// opening a gap at the top for an incoming node (or nodes).
func ShiftRanksAll(db *DB, parentUUID string, delta int) error {
	if _, err := db.Exec("UPDATE nodes SET rank = rank + ? WHERE parent_uuid = ? AND deleted = 0",
		delta, parentUUID); err != nil {
		return errors.Wrapf(err, "shifting ranks under %s", parentUUID)
	}
	return nil
}

// ShiftRanksAfter adds delta to the rank of every non-deleted child of the parent
// whose rank is strictly greater than afterRank, opening a gap after that sibling.
func ShiftRanksAfter(db *DB, parentUUID string, afterRank, delta int) error {
	if _, err := db.Exec("UPDATE nodes SET rank = rank + ? WHERE parent_uuid = ? AND rank > ? AND deleted = 0",
		delta, parentUUID, afterRank); err != nil {
		return errors.Wrapf(err, "shifting ranks under %s", parentUUID)
	}
	return nil
}

// MarkSubtreeDeleted tombstones the node and all descendants.
func MarkSubtreeDeleted(db *DB, rootUUID string) (int, error) {
	subtree, err := GetSubtree(db, rootUUID)
	if err != nil {
		return 0, err
	}
	for _, n := range subtree {
		if _, err := db.Exec("UPDATE nodes SET deleted = 1 WHERE uuid = ?", n.UUID); err != nil {
			return 0, errors.Wrapf(err, "tombstoning node %s", n.UUID)
		}
	}
	return len(subtree), nil
}

// SubtreeCounts returns the subtree size of EVERY non-deleted node, including
// itself, in one pass.
//
// It exists because asking the same question one node at a time is quadratic:
// CountSubtree materializes a whole subtree to take its length, so a finder that
// counted each of its candidates read a large share of the outline once per
// candidate — most of them for rows that never reach the screen. Here the shape
// of the tree is read once and the counting is arithmetic.
func SubtreeCounts(db *DB) (map[string]int, error) {
	rows, err := db.Query(`SELECT uuid, parent_uuid FROM nodes WHERE deleted = 0`)
	if err != nil {
		return nil, errors.Wrap(err, "querying node parents")
	}
	defer rows.Close()

	parent := map[string]string{}
	for rows.Next() {
		var uuid, par string
		if err := rows.Scan(&uuid, &par); err != nil {
			return nil, errors.Wrap(err, "scanning node parent")
		}
		parent[uuid] = par
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating node parents")
	}

	// every node counts itself, then adds itself to each of its ancestors. Walking
	// UP from each node costs one step per edge above it and needs no recursion,
	// and the seen set makes a parent cycle — which a corrupt tree can hold —
	// terminate rather than spin.
	counts := make(map[string]int, len(parent))
	for uuid := range parent {
		counts[uuid]++
		seen := map[string]bool{uuid: true}
		for p := parent[uuid]; p != "" && !seen[p]; p = parent[p] {
			seen[p] = true
			if _, ok := parent[p]; !ok {
				break // an ancestor that is deleted or absent ends the chain
			}
			counts[p]++
		}
	}
	return counts, nil
}

// CountSubtree returns the number of non-deleted nodes in the subtree
// including the root. For more than one node at a time, use SubtreeCounts.
func CountSubtree(db *DB, rootUUID string) (int, error) {
	subtree, err := GetSubtree(db, rootUUID)
	if err != nil {
		return 0, err
	}
	return len(subtree), nil
}

// RecentNodes returns every non-deleted node ordered by recency, excluding the
// fixed root. The editor finder lists it while the query is still empty so
// the picker starts full instead of blank; the picker windows the display
// itself, so no row limit is applied here.
func RecentNodes(db *DB) ([]Node, error) {
	rows, err := db.Query(`WITH RECURSIVE tt(uuid) AS (
		SELECT ?
		UNION ALL
		SELECT n.uuid FROM nodes n JOIN tt ON n.parent_uuid = tt.uuid
	)
	SELECT `+nodeColumns+` FROM nodes
	WHERE deleted = 0 AND uuid != ? AND uuid NOT IN (SELECT uuid FROM tt)
	ORDER BY edited_on DESC`,
		TempUUID, RootUUID)
	if err != nil {
		return nil, errors.Wrap(err, "querying recent nodes")
	}
	defer rows.Close()

	var ret []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, errors.Wrap(err, "scanning node")
		}
		ret = append(ret, n)
	}
	return ret, nil
}

// SearchNodes returns nodes matching the query, best match first. It combines
// FTS5 ranking with simple lexical preferences (exact name, prefix, substring)
// so that the top result is the "most probable" node for best-match commands.
// Starred nodes float to the top of the hit list; relevance order is preserved
// inside each half (starred / unstarred).
func SearchNodes(db *DB, query string, includeCompleted bool) ([]Node, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	tempUUIDs, err := TempSubtreeUUIDs(db)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var ret []Node

	appendNode := func(n Node) {
		if seen[n.UUID] || tempUUIDs[n.UUID] { // never surface the lab in notebook search
			return
		}
		seen[n.UUID] = true
		if !includeCompleted && n.CompletedAt > 0 {
			return
		}
		ret = append(ret, n)
	}

	// lexical pass: exact name, then prefix, then substring
	for _, pattern := range []string{q, q + "%", "%" + q + "%"} {
		rows, err := db.Query("SELECT "+nodeColumns+" FROM nodes WHERE deleted = 0 AND name LIKE ? ORDER BY edited_on DESC LIMIT 50", pattern)
		if err != nil {
			return nil, errors.Wrap(err, "lexical node search")
		}
		for rows.Next() {
			n, err := scanNode(rows)
			if err != nil {
				rows.Close()
				return nil, errors.Wrap(err, "scanning node")
			}
			appendNode(n)
		}
		rows.Close()
	}

	// FTS pass over name+note for word matches the LIKE pass missed
	ftsQuery := buildFTSQuery(q)
	if ftsQuery != "" {
		rows, err := db.Query(`SELECT `+nodeColumns+` FROM nodes
			WHERE deleted = 0 AND id IN (SELECT rowid FROM node_fts WHERE node_fts MATCH ? ORDER BY rank LIMIT 50)`, ftsQuery)
		if err == nil {
			for rows.Next() {
				n, scanErr := scanNode(rows)
				if scanErr != nil {
					rows.Close()
					return nil, errors.Wrap(scanErr, "scanning node")
				}
				appendNode(n)
			}
			rows.Close()
		}
		// FTS syntax errors on odd queries are non-fatal; lexical results stand.
	}

	// chip pass: anchors hide chip content (e.g. a path's basename) from the LIKE
	// and FTS passes, which see only the opaque anchor. Resolve anchors for the
	// anchor-bearing nodes and match the display + full value. char(65532) is the
	// anchor sentinel U+FFFC, so this stays off chipless nodes.
	//
	// The candidates are buffered before LoadChips runs: on a single-connection
	// pool (the daemon's store) a second query while these rows are still open
	// deadlocks, because the one connection is already busy streaming them.
	if rows, err := db.Query("SELECT " + nodeColumns + " FROM nodes WHERE deleted = 0 AND instr(name, char(65532)) > 0 LIMIT 200"); err == nil {
		var anchored []Node
		for rows.Next() {
			n, scanErr := scanNode(rows)
			if scanErr != nil {
				rows.Close()
				return nil, errors.Wrap(scanErr, "scanning node")
			}
			anchored = append(anchored, n)
		}
		rows.Close()
		if len(anchored) > 0 {
			chips, _ := LoadChips(db)
			lq := strings.ToLower(q)
			for _, n := range anchored {
				hay := strings.ToLower(DisplayAnchors(n.Name, chips) + " " + ExpandAnchors(n.Name, chips))
				if strings.Contains(hay, lq) {
					appendNode(n)
				}
			}
		}
	}

	// /star pins: starred hits float above the rest; SliceStable keeps the
	// exact→prefix→substring→FTS relevance order intact inside each half
	sort.SliceStable(ret, func(i, j int) bool {
		return ret[i].Starred && !ret[j].Starred
	})
	return ret, nil
}

// StreamLiveNodes visits every non-deleted node outside the Temporary Domain in
// small batches. Returning false from yield stops the scan. Query views use this
// rather than first assembling a giant candidate slice, so opening a query stays
// responsive while its result set arrives.
func StreamLiveNodes(db *DB, batchSize int, yield func([]Node) bool) error {
	if batchSize < 1 {
		batchSize = 1
	}
	tempUUIDs, err := TempSubtreeUUIDs(db)
	if err != nil {
		return err
	}
	rows, err := db.Query("SELECT " + nodeColumns + " FROM nodes WHERE deleted = 0 ORDER BY added_on DESC")
	if err != nil {
		return errors.Wrap(err, "listing live nodes")
	}
	defer rows.Close()

	batch := make([]Node, 0, batchSize)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return errors.Wrap(err, "scanning node")
		}
		if tempUUIDs[n.UUID] || n.Name == "" {
			continue
		}
		batch = append(batch, n)
		if len(batch) == batchSize {
			if !yield(batch) {
				return nil
			}
			batch = make([]Node, 0, batchSize)
		}
	}
	if err := rows.Err(); err != nil {
		return errors.Wrap(err, "iterating live nodes")
	}
	if len(batch) > 0 {
		yield(batch)
	}
	return nil
}

// AllLiveNodes returns every non-deleted node outside the Temporary Domain — the
// candidate set for a pure time-filtered query, which carries no text for the
// FTS/LIKE passes to use. StreamLiveNodes batches the scan so a large forest does
// not block the editor while it is being read.
func AllLiveNodes(db *DB) ([]Node, error) {
	var ret []Node
	err := StreamLiveNodes(db, 500, func(batch []Node) bool {
		ret = append(ret, batch...)
		return true
	})
	return ret, err
}

// buildFTSQuery turns free text into a safe FTS5 prefix query.
func buildFTSQuery(q string) string {
	fields := strings.Fields(q)
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		terms = append(terms, `"`+f+`"*`)
	}
	return strings.Join(terms, " ")
}
