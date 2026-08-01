package database

import "github.com/pkg/errors"

// The zotero_nodes table binds a node to the Zotero object it mirrors — the map
// behind the Zotero item node type. One row per node of a mirrored item: the
// entry itself, an attachment, an annotation, or a note. A refresh reconciles
// against these keys rather than against text, and each node's own key is what
// its alt+g opens.

// Zotero binding kinds. The kind decides what a mirrored node IS, and so what
// it renders as and where it opens.
const (
	ZoteroKindItem       = "item"
	ZoteroKindAttachment = "attachment"
	ZoteroKindAnnotation = "annotation"
	ZoteroKindNote       = "note"
	ZoteroKindComment    = "comment" // what you typed next to a mark; hangs under it
	ZoteroKindMeta       = "meta"    // a field row (venue, date, doi) — no Zotero object of its own
)

// ZoteroBinding is one node's link to Zotero.
type ZoteroBinding struct {
	Key     string // the Zotero object's key ("" for a meta row)
	Kind    string // one of the ZoteroKind* constants
	GroupID string // group library id; "" = the personal library
}

// UpsertZoteroNode records (or refreshes) a node's Zotero binding.
func UpsertZoteroNode(db *DB, nodeUUID string, b ZoteroBinding, syncedAt int64) error {
	_, err := db.Exec(`INSERT INTO zotero_nodes (node_uuid, item_key, kind, group_id, synced_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(node_uuid) DO UPDATE SET
			item_key = excluded.item_key, kind = excluded.kind,
			group_id = excluded.group_id, synced_at = excluded.synced_at`,
		nodeUUID, b.Key, b.Kind, b.GroupID, syncedAt)
	return errors.Wrap(err, "upserting zotero node map")
}

// DeleteZoteroNode drops a node's Zotero binding (the node left the mirror).
func DeleteZoteroNode(db *DB, nodeUUID string) error {
	_, err := db.Exec("DELETE FROM zotero_nodes WHERE node_uuid = ?", nodeUUID)
	return errors.Wrap(err, "deleting zotero node map")
}

// AllZoteroNodes loads the whole mirror map: node uuid → binding.
func AllZoteroNodes(db *DB) (map[string]ZoteroBinding, error) {
	rows, err := db.Query("SELECT node_uuid, item_key, kind, group_id FROM zotero_nodes")
	if err != nil {
		return nil, errors.Wrap(err, "querying zotero node map")
	}
	defer rows.Close()
	out := map[string]ZoteroBinding{}
	for rows.Next() {
		var uuid string
		var b ZoteroBinding
		if err := rows.Scan(&uuid, &b.Key, &b.Kind, &b.GroupID); err != nil {
			return nil, errors.Wrap(err, "scanning zotero node map")
		}
		out[uuid] = b
	}
	return out, errors.Wrap(rows.Err(), "iterating zotero node map")
}

// GCZoteroNodes drops binding rows whose node is gone — a mirror the user
// deleted, or one that lost a race with live sync. The mirror map is only ever
// as interesting as the nodes it points at.
func GCZoteroNodes(db *DB) error {
	_, err := db.Exec(`DELETE FROM zotero_nodes WHERE node_uuid NOT IN (
		SELECT uuid FROM nodes WHERE deleted = 0
	)`)
	return errors.Wrap(err, "gc zotero node map")
}
