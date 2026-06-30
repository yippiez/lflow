package database

import (
	"database/sql"

	"github.com/pkg/errors"
)

// Artifact is the embedded content snapshot of a file chip — the bytes of an
// .html or .md file captured into the DB when the chip is created (see the
// per-extension file-chip behavior in pkg/tui/editor/file.go). Keyed by the
// chip id, so the content lives independent of the on-disk file. Opening an
// .html chip writes the snapshot to a cache file and points the browser at it.
//
// Local-only, never synced: the content can be large and is machine-specific,
// like the node_output and chips tables (see the sync-exclusion invariant).
type Artifact struct {
	ID      string `json:"id"`      // the chip id this snapshot belongs to
	Name    string `json:"name"`    // human label, e.g. the original file's base name
	Kind    string `json:"kind"`    // "html" or "md"
	Content string `json:"content"` // the raw file bytes
}

// UpsertArtifact inserts or overwrites an artifact by id.
func UpsertArtifact(db *DB, a Artifact) error {
	_, err := db.Exec(
		`INSERT INTO artifacts (id, name, kind, content) VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET name = excluded.name, kind = excluded.kind, content = excluded.content`,
		a.ID, a.Name, a.Kind, a.Content)
	return errors.Wrapf(err, "upserting artifact %s", a.ID)
}

// GetArtifact returns one artifact by id.
func GetArtifact(db *DB, id string) (Artifact, error) {
	var a Artifact
	err := db.QueryRow("SELECT id, name, kind, content FROM artifacts WHERE id = ?", id).
		Scan(&a.ID, &a.Name, &a.Kind, &a.Content)
	if err == sql.ErrNoRows {
		return a, errors.Errorf("artifact %s not found", id)
	}
	return a, errors.Wrapf(err, "getting artifact %s", id)
}

// DeleteArtifact removes an artifact by id.
func DeleteArtifact(db *DB, id string) error {
	_, err := db.Exec("DELETE FROM artifacts WHERE id = ?", id)
	return errors.Wrapf(err, "deleting artifact %s", id)
}

// GCArtifacts drops artifact rows no live node name references through its chip
// anchor — orphans left when the chip's node was deleted or rewritten. Mirrors
// GCChips (the chip id is embedded verbatim in the anchor, so instr matches).
func GCArtifacts(db *DB) error {
	_, err := db.Exec(`DELETE FROM artifacts WHERE id NOT IN (
		SELECT artifacts.id FROM artifacts JOIN nodes ON nodes.deleted = 0 AND instr(nodes.name, artifacts.id) > 0
	)`)
	return errors.Wrap(err, "gc artifacts")
}
