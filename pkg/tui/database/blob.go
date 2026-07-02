package database

import (
	"database/sql"

	"github.com/pkg/errors"
)

// Blob is a node's binary payload — currently an image node's PNG pixels — stored
// in node_blobs keyed by the node uuid. Keeping it in the DB (not a local file)
// keeps the whole outline a single portable SQLite file: copy the .db and the
// images travel with it. It lives in its own table so multi-KB blobs never touch
// the hot nodes scan or the FTS triggers.
type Blob struct {
	UUID  string
	Mime  string
	Bytes []byte
	W, H  int
}

// PutBlob inserts or replaces a node's blob.
func PutBlob(db *DB, b Blob) error {
	_, err := db.Exec(
		`INSERT INTO node_blobs (uuid, mime, bytes, w, h) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(uuid) DO UPDATE SET mime = excluded.mime, bytes = excluded.bytes, w = excluded.w, h = excluded.h`,
		b.UUID, b.Mime, b.Bytes, b.W, b.H)
	return errors.Wrapf(err, "putting blob %s", b.UUID)
}

// GetBlob returns a node's blob. ok=false (nil error) means there is none.
func GetBlob(db *DB, uuid string) (Blob, bool, error) {
	var b Blob
	err := db.QueryRow("SELECT uuid, mime, bytes, w, h FROM node_blobs WHERE uuid = ?", uuid).
		Scan(&b.UUID, &b.Mime, &b.Bytes, &b.W, &b.H)
	if err == sql.ErrNoRows {
		return Blob{}, false, nil
	}
	if err != nil {
		return Blob{}, false, errors.Wrapf(err, "getting blob %s", uuid)
	}
	return b, true, nil
}

// DeleteBlob removes a node's blob.
func DeleteBlob(db *DB, uuid string) error {
	_, err := db.Exec("DELETE FROM node_blobs WHERE uuid = ?", uuid)
	return errors.Wrapf(err, "deleting blob %s", uuid)
}

// GCBlobs drops blobs whose node no longer exists (deleted or expunged), mirroring
// GCChips. Run it after a save, once tombstones and new nodes are persisted.
func GCBlobs(db *DB) error {
	_, err := db.Exec("DELETE FROM node_blobs WHERE uuid NOT IN (SELECT uuid FROM nodes WHERE deleted = 0)")
	return errors.Wrap(err, "gc blobs")
}
