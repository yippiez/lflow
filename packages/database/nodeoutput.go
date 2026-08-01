package database

import (
	"database/sql"

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
