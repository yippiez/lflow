package database

import (
	"github.com/pkg/errors"
)

// Artifact is one row of the LEGACY artifacts table — the pre-file home of
// runtime node types. Definitions now live as one JS file per type in
// <config>/lflow/nodes (see pkg/tui/editor/genui.go); the editor reads this
// table exactly once, to export any existing rows into that directory, and
// never writes it again.
type Artifact struct {
	Key     string // the nodes.type string the program serves
	Source  string // the JS program
	Enabled bool
}

// ListArtifacts returns the legacy rows for the one-time export to files. A
// DB without the table (or with none) simply yields nothing.
func ListArtifacts(db *DB) ([]Artifact, error) {
	rows, err := db.Query("SELECT key, source, enabled FROM artifacts ORDER BY created_by = 'seed' DESC, created_at, key")
	if err != nil {
		return nil, errors.Wrap(err, "querying artifacts")
	}
	defer rows.Close()

	var ret []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.Key, &a.Source, &a.Enabled); err != nil {
			return nil, errors.Wrap(err, "scanning artifact")
		}
		ret = append(ret, a)
	}
	return ret, rows.Err()
}
