package database

import "github.com/pkg/errors"

// Chip is an inline structured token referenced by an anchor in a node's name
// (see the chip-kind registry in pkg/tui/editor). The name text holds an opaque
// anchor carrying the chip id; the chip's real data lives here. Local content
// for now — a path chip's value is a machine-specific absolute path.
type Chip struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`  // path, date, tag, …
	Value string `json:"value"` // the full underlying data (e.g. the absolute path)
}

// LoadChips returns every chip keyed by id.
func LoadChips(db *DB) (map[string]Chip, error) {
	rows, err := db.Query("SELECT id, kind, value FROM chips")
	if err != nil {
		return nil, errors.Wrap(err, "loading chips")
	}
	defer rows.Close()
	out := map[string]Chip{}
	for rows.Next() {
		var c Chip
		if err := rows.Scan(&c.ID, &c.Kind, &c.Value); err != nil {
			return nil, errors.Wrap(err, "scanning chip")
		}
		out[c.ID] = c
	}
	return out, nil
}

// GetChip returns one chip by id.
func GetChip(db *DB, id string) (Chip, error) {
	var c Chip
	err := db.QueryRow("SELECT id, kind, value FROM chips WHERE id = ?", id).Scan(&c.ID, &c.Kind, &c.Value)
	return c, errors.Wrapf(err, "getting chip %s", id)
}

// UpsertChip inserts or overwrites a chip.
func UpsertChip(db *DB, c Chip) error {
	_, err := db.Exec(
		"INSERT INTO chips (id, kind, value) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, value = excluded.value",
		c.ID, c.Kind, c.Value)
	return errors.Wrapf(err, "upserting chip %s", c.ID)
}

// DeleteChip removes a chip by id.
func DeleteChip(db *DB, id string) error {
	_, err := db.Exec("DELETE FROM chips WHERE id = ?", id)
	return errors.Wrapf(err, "deleting chip %s", id)
}

// GCChips drops chip rows no live node name references — orphans left by deleted
// or rewritten nodes. Anchors embed the id verbatim, so an instr match suffices.
func GCChips(db *DB) error {
	_, err := db.Exec(`DELETE FROM chips WHERE id NOT IN (
		SELECT chips.id FROM chips JOIN nodes ON nodes.deleted = 0 AND instr(nodes.name, chips.id) > 0
	)`)
	return errors.Wrap(err, "gc chips")
}
