package database

import (
	"database/sql"

	"github.com/pkg/errors"
)

// Artifact is a self-contained web page (an .html file, or a .md file rendered
// to html) embedded in the DB. A node of type "artifact" keys its artifact by
// the node's own uuid; an inline artifact chip keys it by the chip id. Either
// way the bytes live here — opening one writes them to a cache file and points
// the browser at it.
//
// Local-only, never synced: the content is machine-rendered and can be large,
// like the node_output and chips tables (see the sync-exclusion invariant).
type Artifact struct {
	ID      string `json:"id"`      // node uuid (artifact node) or chip id (artifact chip)
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

// GCArtifacts drops artifact rows nothing live references — neither a live
// artifact node (id = node uuid) nor a chip anchor embedded in a live node name
// (id = chip id). Mirrors GCChips.
func GCArtifacts(db *DB) error {
	_, err := db.Exec(`DELETE FROM artifacts WHERE id NOT IN (
		SELECT uuid FROM nodes WHERE deleted = 0 AND type = 'artifact'
		UNION
		SELECT artifacts.id FROM artifacts JOIN nodes ON nodes.deleted = 0 AND instr(nodes.name, artifacts.id) > 0
	)`)
	return errors.Wrap(err, "gc artifacts")
}
