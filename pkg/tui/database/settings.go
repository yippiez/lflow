package database

import (
	"github.com/pkg/errors"
)

// Settings are global editor preferences (theme, image preview mode, …) stored as
// key/value rows in the settings table. They are local UI state — never synced.

// LoadSettings returns every stored setting as a key→value map.
func LoadSettings(db *DB) (map[string]string, error) {
	rows, err := db.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, errors.Wrap(err, "loading settings")
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, errors.Wrap(err, "scanning setting")
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSetting inserts or updates one setting.
func SetSetting(db *DB, key, value string) error {
	_, err := db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	return errors.Wrapf(err, "setting %s", key)
}
