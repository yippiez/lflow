package database

import (
	"time"

	"github.com/pkg/errors"
)

// RootUUID is the uuid of the always-present root node. Top-level user nodes
// are its children; commands that take no explicit node default to it.
const RootUUID = "root"

// TempUUID is the uuid of the always-present Temporary Domain root — a second
// top-level root, sibling to RootUUID. Its subtree is the temp/agent space:
// persisted and synced like the rest, but swept on startup (7-day retention).
const TempUUID = "temp"

// TempRetention is how long a temp entry survives unchanged before the startup
// sweep removes it.
const TempRetention = 7 * 24 * time.Hour

// EnsureRoot guarantees the root node exists and that every orphan top-level
// node (parent_uuid = "" other than root/temp) is adopted under it. The root is
// local-only and never synced (it is not marked dirty).
func EnsureRoot(db *DB) error {
	var exists int
	if err := db.QueryRow("SELECT count(*) FROM nodes WHERE uuid = ?", RootUUID).Scan(&exists); err != nil {
		return errors.Wrap(err, "checking for root node")
	}
	if exists == 0 {
		if _, err := db.Exec(`INSERT INTO nodes (uuid, parent_uuid, rank, name, type, dirty)
			VALUES (?, '', 0, 'Root', 'bullets', 0)`, RootUUID); err != nil {
			return errors.Wrap(err, "creating root node")
		}
	}

	// adopt any pre-existing top-level nodes under root so listings and the
	// editor see a single forest below root — but never the temp root, which is
	// its own top-level sibling.
	if _, err := db.Exec("UPDATE nodes SET parent_uuid = ? WHERE parent_uuid = '' AND uuid != ? AND uuid != ?", RootUUID, RootUUID, TempUUID); err != nil {
		return errors.Wrap(err, "adopting orphan top-level nodes")
	}

	return nil
}

// EnsureTemp guarantees the Temporary Domain root exists. Like the root it is a
// local-only structural node (dirty = 0, never synced); its children are normal
// nodes that do sync.
func EnsureTemp(db *DB) error {
	var exists int
	if err := db.QueryRow("SELECT count(*) FROM nodes WHERE uuid = ?", TempUUID).Scan(&exists); err != nil {
		return errors.Wrap(err, "checking for temp root")
	}
	if exists == 0 {
		if _, err := db.Exec(`INSERT INTO nodes (uuid, parent_uuid, rank, name, type, dirty)
			VALUES (?, '', 1, 'temp', 'root', 0)`, TempUUID); err != nil {
			return errors.Wrap(err, "creating temp root")
		}
	}
	return nil
}

// TempSubtreeUUIDs returns the set of uuids in the temp subtree (the temp root
// and all its descendants), so notebook finders can exclude the lab.
func TempSubtreeUUIDs(db *DB) (map[string]bool, error) {
	rows, err := db.Query(`WITH RECURSIVE tt(uuid) AS (
		SELECT ?
		UNION ALL
		SELECT n.uuid FROM nodes n JOIN tt ON n.parent_uuid = tt.uuid
	) SELECT uuid FROM tt`, TempUUID)
	if err != nil {
		return nil, errors.Wrap(err, "querying temp subtree")
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, errors.Wrap(err, "scanning temp uuid")
		}
		set[u] = true
	}
	return set, rows.Err()
}

// SweepTempExpired tombstones each top-level temp entry whose entire subtree has
// been unchanged for longer than maxAge. Freshness is the newest edited_on
// across the entry's real (parent_uuid) descendants, so touching anything inside
// keeps the whole entry alive. Returns the number of nodes tombstoned.
func SweepTempExpired(db *DB, maxAge time.Duration) (int, error) {
	cutoff := time.Now().Add(-maxAge).UnixNano()
	entries, err := GetChildren(db, TempUUID)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		subtree, err := GetSubtree(db, e.UUID)
		if err != nil {
			return removed, err
		}
		newest := int64(0)
		for _, n := range subtree {
			if n.EditedOn > newest {
				newest = n.EditedOn
			}
		}
		if newest >= cutoff {
			continue // touched within the window — keep the whole entry
		}
		n, err := MarkSubtreeDeleted(db, e.UUID)
		if err != nil {
			return removed, err
		}
		removed += n
	}
	return removed, nil
}
