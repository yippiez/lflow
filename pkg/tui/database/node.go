package database

import (
	"database/sql"
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
	TypeJSON    = "json"
	// TypeBash is LEGACY: the bash node type was removed 2026-07-09 in favor of
	// inline cmd chips ("$cmd" + double space). Old rows keep the value and
	// render as bullets; it is no longer in TypeOrder/ValidTypes.
	TypeBash  = "bash"
	TypeQuery = "query"
	TypeVoice   = "voice"
	TypeImage   = "image"
	TypeDivider = "divider"
	TypeAgent   = "agent" // an agent-authored reply node: red ✦, text + chips only
	TypeWF      = "wf"    // a Workflowy mirror root: alt+r pulls its subtree (see pkg/tui/wf)
)

// TypeOrder is the canonical ordering of node types — the single source of
// truth for the accepted set. ValidTypes and the human-readable list in CLI
// help/errors derive from it, so a new type added here needs no other edits.
// TypeAgent is a valid stored type (agent-authored replies) but is not offered
// in the /type picker — the editor's picker derives from the registry, not this.
var TypeOrder = []string{
	TypeBullets,
	TypeTodo,
	TypeDivider,
	TypeH1,
	TypeH2,
	TypeH3,
	TypeCode,
	TypeQuote,
	TypeJSON,
	TypeQuery,
	TypeVoice,
	TypeImage,
	TypeAgent,
	TypeWF,
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
	Readonly    bool   `json:"readonly"`  // node lock; persisted (like style)
	Starred     bool   `json:"starred"`   // /star: pinned to the top of pickers
}

const nodeColumns = "uuid, parent_uuid, rank, name, note, type, style, mirror_of, completed_at, added_on, edited_on, deleted, collapsed, readonly, starred"

func scanNode(row interface{ Scan(...interface{}) error }) (Node, error) {
	var n Node
	err := row.Scan(&n.UUID, &n.ParentUUID, &n.Rank, &n.Name, &n.Note, &n.Type,
		&n.Style, &n.MirrorOf, &n.CompletedAt, &n.AddedOn, &n.EditedOn, &n.Deleted, &n.Collapsed, &n.Readonly, &n.Starred)
	return n, err
}

// Insert inserts the node.
func (n Node) Insert(db *DB) error {
	_, err := db.Exec("INSERT INTO nodes ("+nodeColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		n.UUID, n.ParentUUID, n.Rank, n.Name, n.Note, n.Type, n.Style, n.MirrorOf, n.CompletedAt,
		n.AddedOn, n.EditedOn, n.Deleted, n.Collapsed, n.Readonly, n.Starred)
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
	_, err := db.Exec("INSERT INTO nodes ("+nodeColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) "+
		`ON CONFLICT(uuid) DO UPDATE SET
			parent_uuid = excluded.parent_uuid, rank = excluded.rank, name = excluded.name,
			note = excluded.note, type = excluded.type, style = excluded.style,
			mirror_of = excluded.mirror_of, completed_at = excluded.completed_at,
			edited_on = excluded.edited_on, collapsed = excluded.collapsed,
			readonly = excluded.readonly, starred = excluded.starred, deleted = 0`,
		n.UUID, n.ParentUUID, n.Rank, n.Name, n.Note, n.Type, n.Style, n.MirrorOf, n.CompletedAt,
		n.AddedOn, n.EditedOn, n.Deleted, n.Collapsed, n.Readonly, n.Starred)
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

// Update persists all mutable fields of the node.
func (n Node) Update(db *DB) error {
	_, err := db.Exec(`UPDATE nodes SET parent_uuid = ?, rank = ?, name = ?, note = ?, type = ?,
		style = ?, mirror_of = ?, completed_at = ?, edited_on = ?, deleted = ?, readonly = ? WHERE uuid = ?`,
		n.ParentUUID, n.Rank, n.Name, n.Note, n.Type, n.Style, n.MirrorOf, n.CompletedAt,
		n.EditedOn, n.Deleted, n.Readonly, n.UUID)
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
// so a node moved in lands at the top of the list rather than the bottom.
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

// CountSubtree returns the number of non-deleted nodes in the subtree
// including the root.
func CountSubtree(db *DB, rootUUID string) (int, error) {
	subtree, err := GetSubtree(db, rootUUID)
	if err != nil {
		return 0, err
	}
	return len(subtree), nil
}

// MatchScore describes a search hit with its relevance.
type MatchScore struct {
	Node  Node
	Score float64
}

// RecentNodes returns non-deleted nodes ordered by recency, excluding the
// fixed root. The editor finder lists it while the query is still empty so
// the picker starts full instead of blank.
func RecentNodes(db *DB, limit int) ([]Node, error) {
	rows, err := db.Query(`WITH RECURSIVE tt(uuid) AS (
		SELECT ?
		UNION ALL
		SELECT n.uuid FROM nodes n JOIN tt ON n.parent_uuid = tt.uuid
	)
	SELECT `+nodeColumns+` FROM nodes
	WHERE deleted = 0 AND uuid != ? AND uuid NOT IN (SELECT uuid FROM tt)
	ORDER BY edited_on DESC LIMIT ?`,
		TempUUID, RootUUID, limit)
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
	if rows, err := db.Query("SELECT " + nodeColumns + " FROM nodes WHERE deleted = 0 AND instr(name, char(65532)) > 0 LIMIT 200"); err == nil {
		chips, _ := LoadChips(db)
		lq := strings.ToLower(q)
		for rows.Next() {
			n, scanErr := scanNode(rows)
			if scanErr != nil {
				rows.Close()
				return nil, errors.Wrap(scanErr, "scanning node")
			}
			hay := strings.ToLower(DisplayAnchors(n.Name, chips) + " " + ExpandAnchors(n.Name, chips))
			if strings.Contains(hay, lq) {
				appendNode(n)
			}
		}
		rows.Close()
	}

	return ret, nil
}

// AllLiveNodes returns every non-deleted node outside the Temporary Domain — the
// candidate set for a pure time-filtered query, which carries no text for the
// FTS/LIKE passes to use. Bounded so a huge forest can't stall the editor.
func AllLiveNodes(db *DB) ([]Node, error) {
	tempUUIDs, err := TempSubtreeUUIDs(db)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query("SELECT " + nodeColumns + " FROM nodes WHERE deleted = 0 ORDER BY added_on DESC LIMIT 500")
	if err != nil {
		return nil, errors.Wrap(err, "listing live nodes")
	}
	defer rows.Close()
	var ret []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, errors.Wrap(err, "scanning node")
		}
		if tempUUIDs[n.UUID] || n.Name == "" {
			continue
		}
		ret = append(ret, n)
	}
	return ret, nil
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
