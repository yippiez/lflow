package database

import (
	"database/sql"
	"strings"

	"github.com/pkg/errors"
)

// A runnable node's captured output (bash/query stdout/stderr) is mirrored into
// node_output — one JSON blob per node uuid — so it survives a restart. It is
// decoupled from the node row (like node_mod_data / node_blobs) so it persists
// the instant a run finishes, before the node itself is saved.
//
// WARNING (invariant): run output is local-only — never synced and never part of
// the node payload. It is not notebook content.

// LoadNodeOutput returns a node's persisted run output JSON, or "" if there is
// none (never run).
func LoadNodeOutput(db *DB, uuid string) (string, error) {
	var out string
	err := db.QueryRow("SELECT output FROM node_output WHERE uuid = ?", uuid).Scan(&out)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return out, errors.Wrapf(err, "loading node output %s", uuid)
}

// LoadNodeOutputs reads many at once, keyed by uuid, skipping the ones with no
// row. One query rather than one per id: the editor snapshots every chip's
// sidecar before a mutating keypress, and that must not cost a round trip each.
func LoadNodeOutputs(db *DB, uuids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(uuids) == 0 {
		return out, nil
	}
	q := "SELECT uuid, output FROM node_output WHERE uuid IN (?" + strings.Repeat(",?", len(uuids)-1) + ")"
	args := make([]interface{}, len(uuids))
	for i, u := range uuids {
		args[i] = u
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, errors.Wrap(err, "loading node outputs")
	}
	defer rows.Close()
	for rows.Next() {
		var uuid, output string
		if err := rows.Scan(&uuid, &output); err != nil {
			return nil, errors.Wrap(err, "scanning node output")
		}
		out[uuid] = output
	}
	return out, errors.Wrap(rows.Err(), "loading node outputs")
}

// SaveNodeOutput stores a node's run output JSON, overwriting any previous run.
func SaveNodeOutput(db *DB, uuid, output string) error {
	_, err := db.Exec(
		"INSERT INTO node_output (uuid, output) VALUES (?, ?) ON CONFLICT(uuid) DO UPDATE SET output = excluded.output",
		uuid, output)
	return errors.Wrapf(err, "saving node output %s", uuid)
}

// DeleteNodeOutput drops a node's persisted run output.
func DeleteNodeOutput(db *DB, uuid string) error {
	_, err := db.Exec("DELETE FROM node_output WHERE uuid = ?", uuid)
	return errors.Wrapf(err, "deleting node output %s", uuid)
}
