package testutils

import (
	"testing"

	"github.com/lflow/lflow/pkg/cli/database"
)

// SetupNodes1 seeds a small node forest:
//
//	experiment results (h1, root)
//	├─ baseline numbers
//	│  ╰─ parse: 1.42s
//	╰─ attempt 2
//	reading list (root)
func SetupNodes1(t *testing.T, db *database.DB) {
	insertNode(t, db, "root-1-uuid", "", 0, "experiment results", "h1", 0)
	insertNode(t, db, "child-1-uuid", "root-1-uuid", 0, "baseline numbers", "bullets", 0)
	insertNode(t, db, "grandchild-1-uuid", "child-1-uuid", 0, "parse: 1.42s", "bullets", 0)
	insertNode(t, db, "child-2-uuid", "root-1-uuid", 1, "attempt 2", "bullets", 0)
	insertNode(t, db, "root-2-uuid", "", 1, "reading list", "bullets", 0)
}

// SetupNodes2 seeds nodes that have already been synced (usn > 0).
func SetupNodes2(t *testing.T, db *database.DB) {
	insertNode(t, db, "root-1-uuid", "", 0, "experiment results", "h1", 11)
	insertNode(t, db, "child-1-uuid", "root-1-uuid", 0, "baseline numbers", "bullets", 12)
	insertNode(t, db, "root-2-uuid", "", 1, "reading list", "bullets", 13)
}

func insertNode(t *testing.T, db *database.DB, uuid, parentUUID string, rank int, name, layout string, usn int) {
	database.MustExec(t, "setting up node "+name, db,
		"INSERT INTO nodes (uuid, parent_uuid, rank, name, note, layout, mirror_of, completed_at, added_on, edited_on, usn, deleted, dirty) VALUES (?, ?, ?, ?, '', ?, '', 0, ?, ?, ?, 0, ?)",
		uuid, parentUUID, rank, name, layout, 1515199943, 1515199943, usn, usn == 0)
}
