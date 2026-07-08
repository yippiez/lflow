package database

import "github.com/pkg/errors"

// The wf_nodes table binds a pulled lflow node to its Workflowy source id — the
// mirror map behind the wf node type. One row per pulled node; the whole table
// is small enough to load at editor start.

// UpsertWFNode records (or refreshes) a node's Workflowy binding.
func UpsertWFNode(db *DB, nodeUUID, wfID string, syncedAt int64) error {
	_, err := db.Exec(`INSERT INTO wf_nodes (node_uuid, wf_id, synced_at) VALUES (?, ?, ?)
		ON CONFLICT(node_uuid) DO UPDATE SET wf_id = excluded.wf_id, synced_at = excluded.synced_at`,
		nodeUUID, wfID, syncedAt)
	return errors.Wrap(err, "upserting wf node map")
}

// DeleteWFNode drops a node's Workflowy binding (the node left the mirror).
func DeleteWFNode(db *DB, nodeUUID string) error {
	_, err := db.Exec("DELETE FROM wf_nodes WHERE node_uuid = ?", nodeUUID)
	return errors.Wrap(err, "deleting wf node map")
}

// AllWFNodes loads the whole mirror map: node uuid → workflowy id.
func AllWFNodes(db *DB) (map[string]string, error) {
	rows, err := db.Query("SELECT node_uuid, wf_id FROM wf_nodes")
	if err != nil {
		return nil, errors.Wrap(err, "querying wf node map")
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var uuid, wfID string
		if err := rows.Scan(&uuid, &wfID); err != nil {
			return nil, errors.Wrap(err, "scanning wf node map")
		}
		out[uuid] = wfID
	}
	return out, rows.Err()
}
