/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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

// InsertSystem inserets a system configuration
func InsertSystem(db *DB, key, val string) error {
	if _, err := db.Exec("INSERT INTO system (key, value) VALUES (? , ?);", key, val); err != nil {
		return errors.Wrap(err, "saving system config")
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
