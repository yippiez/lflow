package database

import (
	"database/sql"

	"github.com/pkg/errors"
)

// Artifact is a runtime-loaded node-type (and chip-kind) plugin: one JS
// program stored in the DB so definitions travel with the outline — a node
// whose type is an artifact renders correctly on any machine, and a
// "forgotten" artifact keeps working years later because it is inseparable
// from the data. See docs/ARTIFACTS.md.
//
// WARNING (invariant): an artifact is data, not schema — installing one is an
// INSERT, never a DB migration. A node whose artifact is missing or disabled
// falls back to bullets via typeOf, so the outline always loads.
type Artifact struct {
	Key       string `json:"key"`   // the nodes.type string this artifact serves
	Label     string `json:"label"` // the /type picker label
	Version   int    `json:"version"`
	Source    string `json:"source"`     // the JS program
	CreatedBy string `json:"created_by"` // 'seed' | 'user' | agent name
	CreatedAt int64  `json:"created_at"`
	Enabled   bool   `json:"enabled"`
}

const artifactColumns = "key, label, version, source, created_by, created_at, enabled"

func scanArtifact(row interface{ Scan(...interface{}) error }) (Artifact, error) {
	var a Artifact
	err := row.Scan(&a.Key, &a.Label, &a.Version, &a.Source, &a.CreatedBy, &a.CreatedAt, &a.Enabled)
	return a, err
}

// Upsert installs the artifact, replacing any existing definition for the same
// key and bumping its version past the old one.
func (a Artifact) Upsert(db *DB) error {
	_, err := db.Exec(`INSERT INTO artifacts (`+artifactColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			label = excluded.label, version = artifacts.version + 1, source = excluded.source,
			created_by = excluded.created_by, enabled = excluded.enabled`,
		a.Key, a.Label, a.Version, a.Source, a.CreatedBy, a.CreatedAt, a.Enabled)
	if err != nil {
		return errors.Wrapf(err, "upserting artifact %s", a.Key)
	}
	return nil
}

// GetArtifact returns the artifact with the given key.
func GetArtifact(db *DB, key string) (Artifact, error) {
	a, err := scanArtifact(db.QueryRow("SELECT "+artifactColumns+" FROM artifacts WHERE key = ?", key))
	if err == sql.ErrNoRows {
		return a, errors.Errorf("artifact %s not found", key)
	} else if err != nil {
		return a, errors.Wrapf(err, "querying artifact %s", key)
	}
	return a, nil
}

// ListArtifacts returns every installed artifact, seeded ones first, then by
// install time — the order the /type picker appends them in.
func ListArtifacts(db *DB) ([]Artifact, error) {
	rows, err := db.Query("SELECT " + artifactColumns + " FROM artifacts ORDER BY created_by = 'seed' DESC, created_at, key")
	if err != nil {
		return nil, errors.Wrap(err, "querying artifacts")
	}
	defer rows.Close()

	var ret []Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, errors.Wrap(err, "scanning artifact")
		}
		ret = append(ret, a)
	}
	return ret, rows.Err()
}

// SetArtifactEnabled flips an artifact's enabled flag.
func SetArtifactEnabled(db *DB, key string, enabled bool) error {
	if _, err := db.Exec("UPDATE artifacts SET enabled = ? WHERE key = ?", enabled, key); err != nil {
		return errors.Wrapf(err, "setting enabled for artifact %s", key)
	}
	return nil
}

// DeleteArtifact removes the artifact row. Nodes of its type stay untouched
// and render as bullets until the type is reinstalled.
func DeleteArtifact(db *DB, key string) error {
	if _, err := db.Exec("DELETE FROM artifacts WHERE key = ?", key); err != nil {
		return errors.Wrapf(err, "deleting artifact %s", key)
	}
	return nil
}

// SeedLogArtifactSource is the JS for the seeded "log" artifact — the one
// built-in that migrated to the artifact model, and the reference program an
// agent-generated artifact is expected to look like. It reproduces the old
// compiled-in behavior exactly: → glyph tinted by /color, a muted
// "(YYYY-MM-DD HH:MM)" time chip, and a muted " · description" tail.
const SeedLogArtifactSource = `lflow.registerType({
    key: "log",
    label: "Log",
    inlineEditable: true,
    glyph: function (node) { return ["→", node.color || "dim"]; },
    baseColor: function (node) { return node.color || "dim"; },
    prefix: function (node) {
        return lflow.style("(" + lflow.time(node.addedOn) + ") ", "dim");
    },
    muteFrom: function (name) { return name.indexOf(" · "); },
});
`
