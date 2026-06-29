package database

import (
	"github.com/pkg/errors"
)

// GetSystem scans the given system configuration record onto the destination
func GetSystem(db *DB, key string, dest interface{}) error {
	if err := db.QueryRow("SELECT value FROM system WHERE key = ?", key).Scan(dest); err != nil {
		return errors.Wrap(err, "finding system configuration record")
	}

	return nil
}

// UpsertSystem inserts or updates a system configuration
func UpsertSystem(db *DB, key, val string) error {
	var count int
	if err := db.QueryRow("SELECT count(*) FROM system WHERE key = ?", key).Scan(&count); err != nil {
		return errors.Wrap(err, "counting system record")
	}

	if count == 0 {
		if _, err := db.Exec("INSERT INTO system (key, value) VALUES (? , ?);", key, val); err != nil {
			return errors.Wrap(err, "saving system config")
		}
	} else {
		if _, err := db.Exec("UPDATE system SET value = ? WHERE key = ?", val, key); err != nil {
			return errors.Wrap(err, "updating system config")
		}
	}

	return nil
}

// UpdateSystem updates a system configuration
func UpdateSystem(db *DB, key, val interface{}) error {
	if _, err := db.Exec("UPDATE system SET value = ? WHERE key = ?", val, key); err != nil {
		return errors.Wrap(err, "updating system config")
	}

	return nil
}

// DeleteSystem delets the given system record
func DeleteSystem(db *DB, key string) error {
	if _, err := db.Exec("DELETE FROM system WHERE key = ?", key); err != nil {
		return errors.Wrap(err, "deleting system config")
	}

	return nil
}
