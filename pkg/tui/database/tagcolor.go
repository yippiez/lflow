package database

import "github.com/pkg/errors"

// tag_colors holds the manual per-tag colors: one row per tag word (lowercase,
// no '#'), value = a style color name. No row means the default muted gray.

// SetTagColor assigns a color to a tag; an empty color removes the assignment.
func SetTagColor(db *DB, tag, color string) error {
	if color == "" {
		_, err := db.Exec("DELETE FROM tag_colors WHERE tag = ?", tag)
		return errors.Wrap(err, "clearing tag color")
	}
	_, err := db.Exec(`INSERT INTO tag_colors (tag, color) VALUES (?, ?)
		ON CONFLICT(tag) DO UPDATE SET color = excluded.color`, tag, color)
	return errors.Wrap(err, "setting tag color")
}

// AllTagColors loads the full tag → color map.
func AllTagColors(db *DB) (map[string]string, error) {
	rows, err := db.Query("SELECT tag, color FROM tag_colors")
	if err != nil {
		return nil, errors.Wrap(err, "querying tag colors")
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tag, color string
		if err := rows.Scan(&tag, &color); err != nil {
			return nil, errors.Wrap(err, "scanning tag color")
		}
		out[tag] = color
	}
	return out, rows.Err()
}
