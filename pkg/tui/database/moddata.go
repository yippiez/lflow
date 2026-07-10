package database

import (
	"database/sql"

	"github.com/pkg/errors"
)

// NodeMod persistent state lives in node_mod_data — one JSON blob per node uuid,
// written by a mod view's lflow.setData and read back by lflow.getData. It is
// decoupled from the node row (like node_output / node_blobs) so it persists the
// instant a mod saves, before the node itself is saved.
//
// WARNING (invariant): mod data is local-only — never synced and never part of
// the node payload. It is per-node UI/app state, not notebook content.

// GetModData returns a node's stored mod data JSON, or "" if there is none.
func GetModData(db *DB, uuid string) (string, error) {
	var data string
	err := db.QueryRow("SELECT data FROM node_mod_data WHERE uuid = ?", uuid).Scan(&data)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return data, errors.Wrapf(err, "getting mod data %s", uuid)
}

// PutModData stores a node's mod data JSON. Empty data deletes the row so a mod
// that clears its state leaves nothing behind.
func PutModData(db *DB, uuid, data string) error {
	if data == "" {
		return DeleteModData(db, uuid)
	}
	_, err := db.Exec(
		`INSERT INTO node_mod_data (uuid, data) VALUES (?, ?)
			ON CONFLICT(uuid) DO UPDATE SET data = excluded.data`,
		uuid, data)
	return errors.Wrapf(err, "putting mod data %s", uuid)
}

// DeleteModData removes a node's mod data.
func DeleteModData(db *DB, uuid string) error {
	_, err := db.Exec("DELETE FROM node_mod_data WHERE uuid = ?", uuid)
	return errors.Wrapf(err, "deleting mod data %s", uuid)
}

// GCModData drops mod data whose node no longer exists, mirroring GCBlobs.
func GCModData(db *DB) error {
	_, err := db.Exec("DELETE FROM node_mod_data WHERE uuid NOT IN (SELECT uuid FROM nodes WHERE deleted = 0)")
	return errors.Wrap(err, "gc mod data")
}
