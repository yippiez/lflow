// Throwaway seeder for the mirror-uncollapse repro. Deleted after the session.
package main

import (
	"fmt"
	"os"

	"github.com/lflow/lflow/pkg/tui/database"
)

func ins(db *database.DB, uuid, parent string, rank int, name, mirrorOf string, collapsed bool) {
	_, err := db.Exec(`INSERT INTO nodes (uuid, parent_uuid, rank, name, note, type, mirror_of, completed_at, added_on, edited_on, deleted, collapsed)
		VALUES (?, ?, ?, ?, '', 'bullets', ?, 0, 1, 1, 0, ?)`, uuid, parent, rank, name, mirrorOf, collapsed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "insert", uuid, err)
		os.Exit(1)
	}
}

func main() {
	db, err := database.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	if err := database.EnsureRoot(db); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	r := database.RootUUID
	// control: plain mirror of a node with children
	ins(db, "src", r, 0, "source", "", false)
	ins(db, "src-a", "src", 0, "alpha", "", false)
	ins(db, "src-b", "src", 1, "beta", "", false)
	ins(db, "m-ok", r, 1, "", "src", true)
	// case B: mirror of a mirror
	ins(db, "m1", r, 2, "", "src", false)
	ins(db, "m2", r, 3, "", "m1", true)
	// case C: mirror pointing at its own ancestor
	ins(db, "anc", r, 4, "ancestor", "", false)
	ins(db, "anc-sub", "anc", 0, "sub", "", false)
	ins(db, "m-anc", "anc-sub", 0, "", "anc", true)
	// case A: mirror inside a subtree whose source lives outside it
	ins(db, "zone", r, 5, "zone", "", false)
	ins(db, "m-out", "zone", 0, "", "elsewhere", true)
	ins(db, "elsewhere", r, 6, "elsewhere", "", false)
	ins(db, "e1", "elsewhere", 0, "e-one", "", false)
	ins(db, "e2", "elsewhere", 1, "e-two", "", false)
	fmt.Println("seeded")
}
