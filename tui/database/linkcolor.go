package database

import "github.com/pkg/errors"

// link_colors holds the manual per-link colors: one row per link CHIP id, value
// = a style color name. No row means the link follows the link.color setting
// (or its service's own hue). Unlike tag colors — keyed by the tag word, so one
// choice paints every "#budget" — a link is an individual object, so the color
// is the chip's own.

// SetLinkColor assigns a color to a link chip; an empty color removes the
// assignment.
func SetLinkColor(db *DB, chipID, color string) error {
	if color == "" {
		_, err := db.Exec("DELETE FROM link_colors WHERE chip_id = ?", chipID)
		return errors.Wrap(err, "clearing link color")
	}
	_, err := db.Exec(`INSERT INTO link_colors (chip_id, color) VALUES (?, ?)
		ON CONFLICT(chip_id) DO UPDATE SET color = excluded.color`, chipID, color)
	return errors.Wrap(err, "setting link color")
}

// AllLinkColors loads the full chip id → color map.
func AllLinkColors(db *DB) (map[string]string, error) {
	rows, err := db.Query("SELECT chip_id, color FROM link_colors")
	if err != nil {
		return nil, errors.Wrap(err, "querying link colors")
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, color string
		if err := rows.Scan(&id, &color); err != nil {
			return nil, errors.Wrap(err, "scanning link color")
		}
		out[id] = color
	}
	return out, rows.Err()
}

// GCLinkColors drops color rows whose chip is gone. Deleting a link chip leaves
// its color row behind on purpose — an undo restores the chip under the SAME id,
// and the color has to come back with it — so this runs at the same point
// GCChips does, once the session's deletions are final.
func GCLinkColors(db *DB) error {
	_, err := db.Exec("DELETE FROM link_colors WHERE chip_id NOT IN (SELECT id FROM chips)")
	return errors.Wrap(err, "gc link colors")
}
