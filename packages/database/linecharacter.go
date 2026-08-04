package database

import "github.com/pkg/errors"

// The Line node type (editor/line.go) attaches a character name to an
// otherwise ordinary text node. line_characters holds one row per Line node
// (node uuid -> character); character_colors holds one row per character name
// (like tag_colors) so the same character reads in the same color everywhere it
// speaks, even after every line naming it has been rewritten or removed.

// SetLineCharacter assigns a character to a Line node; an empty character
// clears the assignment.
func SetLineCharacter(db *DB, nodeUUID, character string) error {
	if character == "" {
		_, err := db.Exec("DELETE FROM line_characters WHERE node_uuid = ?", nodeUUID)
		return errors.Wrap(err, "clearing line character")
	}
	_, err := db.Exec(`INSERT INTO line_characters (node_uuid, character) VALUES (?, ?)
		ON CONFLICT(node_uuid) DO UPDATE SET character = excluded.character`, nodeUUID, character)
	return errors.Wrap(err, "setting line character")
}

// AllLineCharacters loads the full node uuid -> character map.
func AllLineCharacters(db *DB) (map[string]string, error) {
	rows, err := db.Query("SELECT node_uuid, character FROM line_characters")
	if err != nil {
		return nil, errors.Wrap(err, "querying line characters")
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var uuid, character string
		if err := rows.Scan(&uuid, &character); err != nil {
			return nil, errors.Wrap(err, "scanning line character")
		}
		out[uuid] = character
	}
	return out, rows.Err()
}

// SetCharacterColor assigns a color to a character name; an empty color
// removes the assignment (back to the default muted gray).
func SetCharacterColor(db *DB, character, color string) error {
	if color == "" {
		_, err := db.Exec("DELETE FROM character_colors WHERE character = ?", character)
		return errors.Wrap(err, "clearing character color")
	}
	_, err := db.Exec(`INSERT INTO character_colors (character, color) VALUES (?, ?)
		ON CONFLICT(character) DO UPDATE SET color = excluded.color`, character, color)
	return errors.Wrap(err, "setting character color")
}

// AllCharacterColors loads the full character -> color map.
func AllCharacterColors(db *DB) (map[string]string, error) {
	rows, err := db.Query("SELECT character, color FROM character_colors")
	if err != nil {
		return nil, errors.Wrap(err, "querying character colors")
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var character, color string
		if err := rows.Scan(&character, &color); err != nil {
			return nil, errors.Wrap(err, "scanning character color")
		}
		out[character] = color
	}
	return out, rows.Err()
}
