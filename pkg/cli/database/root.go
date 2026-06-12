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
	"database/sql"

	"github.com/pkg/errors"
)

// RootUUID is the uuid of the always-present root node. Top-level user nodes
// are its children; commands that take no explicit node default to it.
const RootUUID = "root"

// EnsureRoot guarantees the root node exists and that every orphan top-level
// node (parent_uuid = ” other than root itself) is adopted under it. The root
// is local-only and never synced (it is not marked dirty).
func EnsureRoot(db *DB) error {
	var exists int
	if err := db.QueryRow("SELECT count(*) FROM nodes WHERE uuid = ?", RootUUID).Scan(&exists); err != nil {
		return errors.Wrap(err, "checking for root node")
	}
	if exists == 0 {
		if _, err := db.Exec(`INSERT INTO nodes (uuid, parent_uuid, rank, name, layout, dirty)
			VALUES (?, '', 0, 'root', 'bullets', 0)`, RootUUID); err != nil {
			return errors.Wrap(err, "creating root node")
		}
	}

	// adopt any pre-existing top-level nodes under root so listings and the
	// editor see a single forest below root
	if _, err := db.Exec("UPDATE nodes SET parent_uuid = ? WHERE parent_uuid = '' AND uuid != ?", RootUUID, RootUUID); err != nil {
		return errors.Wrap(err, "adopting orphan top-level nodes")
	}

	return nil
}

// RootNode returns the root node, creating it if necessary.
func RootNode(db *DB) (Node, error) {
	if err := EnsureRoot(db); err != nil {
		return Node{}, err
	}
	n, err := GetNode(db, RootUUID)
	if err == sql.ErrNoRows {
		return n, errors.New("root node missing after EnsureRoot")
	}
	return n, err
}
