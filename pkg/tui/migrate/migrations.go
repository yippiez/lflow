package migrate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dnote/actions"
	"github.com/lflow/lflow/pkg/tui/config"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/pkg/errors"
)

type migration struct {
	name string
	run  func(ctx context.DnoteCtx, tx *database.DB) error
}

var lm1 = migration{
	name: "upgrade-edit-note-from-v1-to-v3",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		rows, err := tx.Query("SELECT uuid, data FROM actions WHERE type = ? AND schema = ?", "edit_note", 1)
		if err != nil {
			return errors.Wrap(err, "querying rows")
		}
		defer rows.Close()

		f := false

		for rows.Next() {
			var uuid, dat string

			err = rows.Scan(&uuid, &dat)
			if err != nil {
				return errors.Wrap(err, "scanning a row")
			}

			var oldData actions.EditNoteDataV1
			err = json.Unmarshal([]byte(dat), &oldData)
			if err != nil {
				return errors.Wrap(err, "unmarshalling existing data")
			}

			newData := actions.EditNoteDataV3{
				NoteUUID: oldData.NoteUUID,
				Content:  &oldData.Content,
				// With edit_note v1, CLI did not support changing books or public
				BookName: nil,
				Public:   &f,
			}

			b, err := json.Marshal(newData)
			if err != nil {
				return errors.Wrap(err, "marshalling new data")
			}

			_, err = tx.Exec("UPDATE actions SET data = ?, schema = ? WHERE uuid = ?", string(b), 3, uuid)
			if err != nil {
				return errors.Wrap(err, "updating a row")
			}
		}

		return nil
	},
}

var lm2 = migration{
	name: "upgrade-edit-note-from-v2-to-v3",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		rows, err := tx.Query("SELECT uuid, data FROM actions WHERE type = ? AND schema = ?", "edit_note", 2)
		if err != nil {
			return errors.Wrap(err, "querying rows")
		}
		defer rows.Close()

		for rows.Next() {
			var uuid, dat string

			err = rows.Scan(&uuid, &dat)
			if err != nil {
				return errors.Wrap(err, "scanning a row")
			}

			var oldData actions.EditNoteDataV2
			err = json.Unmarshal([]byte(dat), &oldData)
			if err != nil {
				return errors.Wrap(err, "unmarshalling existing data")
			}

			newData := actions.EditNoteDataV3{
				NoteUUID: oldData.NoteUUID,
				BookName: oldData.ToBook,
				Content:  oldData.Content,
				Public:   oldData.Public,
			}

			b, err := json.Marshal(newData)
			if err != nil {
				return errors.Wrap(err, "marshalling new data")
			}

			_, err = tx.Exec("UPDATE actions SET data = ?, schema = ? WHERE uuid = ?", string(b), 3, uuid)
			if err != nil {
				return errors.Wrap(err, "updating a row")
			}
		}

		return nil
	},
}

var lm3 = migration{
	name: "upgrade-remove-note-from-v1-to-v2",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		rows, err := tx.Query("SELECT uuid, data FROM actions WHERE type = ? AND schema = ?", "remove_note", 1)
		if err != nil {
			return errors.Wrap(err, "querying rows")
		}
		defer rows.Close()

		for rows.Next() {
			var uuid, dat string

			err = rows.Scan(&uuid, &dat)
			if err != nil {
				return errors.Wrap(err, "scanning a row")
			}

			var oldData actions.RemoveNoteDataV1
			err = json.Unmarshal([]byte(dat), &oldData)
			if err != nil {
				return errors.Wrap(err, "unmarshalling existing data")
			}

			newData := actions.RemoveNoteDataV2{
				NoteUUID: oldData.NoteUUID,
			}

			b, err := json.Marshal(newData)
			if err != nil {
				return errors.Wrap(err, "marshalling new data")
			}

			_, err = tx.Exec("UPDATE actions SET data = ?, schema = ? WHERE uuid = ?", string(b), 2, uuid)
			if err != nil {
				return errors.Wrap(err, "updating a row")
			}
		}

		return nil
	},
}

var lm4 = migration{
	name: "add-dirty-usn-deleted-to-notes-and-books",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		_, err := tx.Exec("ALTER TABLE books ADD COLUMN dirty bool DEFAULT false")
		if err != nil {
			return errors.Wrap(err, "adding dirty column to books")
		}

		_, err = tx.Exec("ALTER TABLE books ADD COLUMN usn int DEFAULT 0 NOT NULL")
		if err != nil {
			return errors.Wrap(err, "adding usn column to books")
		}

		_, err = tx.Exec("ALTER TABLE books ADD COLUMN deleted bool DEFAULT false")
		if err != nil {
			return errors.Wrap(err, "adding deleted column to books")
		}

		_, err = tx.Exec("ALTER TABLE notes ADD COLUMN dirty bool DEFAULT false")
		if err != nil {
			return errors.Wrap(err, "adding dirty column to notes")
		}

		_, err = tx.Exec("ALTER TABLE notes ADD COLUMN usn int DEFAULT 0 NOT NULL")
		if err != nil {
			return errors.Wrap(err, "adding usn column to notes")
		}

		_, err = tx.Exec("ALTER TABLE notes ADD COLUMN deleted bool DEFAULT false")
		if err != nil {
			return errors.Wrap(err, "adding deleted column to notes")
		}

		return nil
	},
}

var lm5 = migration{
	name: "mark-action-targets-dirty",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		rows, err := tx.Query("SELECT uuid, data, type FROM actions")
		if err != nil {
			return errors.Wrap(err, "querying rows")
		}
		defer rows.Close()

		for rows.Next() {
			var uuid, dat, actionType string

			err = rows.Scan(&uuid, &dat, &actionType)
			if err != nil {
				return errors.Wrap(err, "scanning a row")
			}

			// removed notes and removed books cannot be reliably derived retrospectively
			// because books did not use to have uuid. Users will find locally deleted
			// notes and books coming back to existence if they have not synced the change.
			// But there will be no data loss.
			switch actionType {
			case "add_note":
				var data actions.AddNoteDataV2
				err = json.Unmarshal([]byte(dat), &data)
				if err != nil {
					return errors.Wrap(err, "unmarshalling existing data")
				}

				_, err := tx.Exec("UPDATE notes SET dirty = true WHERE uuid = ?", data.NoteUUID)
				if err != nil {
					return errors.Wrapf(err, "markig note dirty '%s'", data.NoteUUID)
				}
			case "edit_note":
				var data actions.EditNoteDataV3
				err = json.Unmarshal([]byte(dat), &data)
				if err != nil {
					return errors.Wrap(err, "unmarshalling existing data")
				}

				_, err := tx.Exec("UPDATE notes SET dirty = true WHERE uuid = ?", data.NoteUUID)
				if err != nil {
					return errors.Wrapf(err, "markig note dirty '%s'", data.NoteUUID)
				}
			case "add_book":
				var data actions.AddBookDataV1
				err = json.Unmarshal([]byte(dat), &data)
				if err != nil {
					return errors.Wrap(err, "unmarshalling existing data")
				}

				_, err := tx.Exec("UPDATE books SET dirty = true WHERE label = ?", data.BookName)
				if err != nil {
					return errors.Wrapf(err, "markig note dirty '%s'", data.BookName)
				}
			}
		}

		return nil
	},
}

var lm6 = migration{
	name: "drop-actions",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		_, err := tx.Exec("DROP TABLE actions;")
		if err != nil {
			return errors.Wrap(err, "dropping the actions table")
		}

		return nil
	},
}

var lm7 = migration{
	name: "resolve-conflicts-with-reserved-book-names",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		migrateBook := func(name string) error {
			var uuid string

			err := tx.QueryRow("SELECT uuid FROM books WHERE label = ?", name).Scan(&uuid)
			if err == sql.ErrNoRows {
				// if not found, noop
				return nil
			} else if err != nil {
				return errors.Wrap(err, "finding trash book")
			}

			for i := 2; ; i++ {
				candidate := fmt.Sprintf("%s (%d)", name, i)

				var count int
				err := tx.QueryRow("SELECT count(*) FROM books WHERE label = ?", candidate).Scan(&count)
				if err != nil {
					return errors.Wrap(err, "counting candidate")
				}

				if count == 0 {
					_, err := tx.Exec("UPDATE books SET label = ?, dirty = ? WHERE uuid = ?", candidate, true, uuid)
					if err != nil {
						return errors.Wrapf(err, "updating book '%s'", name)
					}

					break
				}
			}

			return nil
		}

		if err := migrateBook("trash"); err != nil {
			return errors.Wrap(err, "migrating trash book")
		}
		if err := migrateBook("conflicts"); err != nil {
			return errors.Wrap(err, "migrating conflicts book")
		}

		return nil
	},
}

var lm8 = migration{
	name: "drop-note-id-and-rename-content-to-body",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		_, err := tx.Exec(`CREATE TABLE notes_tmp
		(
			uuid text NOT NULL,
			book_uuid text NOT NULL,
			body text NOT NULL,
			added_on integer NOT NULL,
			edited_on integer DEFAULT 0,
			public bool DEFAULT false,
			dirty bool DEFAULT false,
			usn int DEFAULT 0 NOT NULL,
			deleted bool DEFAULT false
		);`)
		if err != nil {
			return errors.Wrap(err, "creating temporary notes table for migration")
		}

		_, err = tx.Exec(`INSERT INTO notes_tmp
			SELECT uuid, book_uuid, content, added_on, edited_on, public, dirty, usn, deleted FROM notes;`)
		if err != nil {
			return errors.Wrap(err, "copying data to new table")
		}

		_, err = tx.Exec(`DROP TABLE notes;`)
		if err != nil {
			return errors.Wrap(err, "dropping the notes table")
		}

		_, err = tx.Exec(`ALTER TABLE notes_tmp RENAME to notes;`)
		if err != nil {
			return errors.Wrap(err, "renaming the temporary notes table")
		}

		return nil
	},
}

var lm9 = migration{
	name: "create-fts-index",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		_, err := tx.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS note_fts USING fts5(content=notes, body, tokenize="porter unicode61 categories 'L* N* Co Ps Pe'");`)
		if err != nil {
			return errors.Wrap(err, "creating note_fts")
		}

		// Create triggers to keep the indices in note_fts in sync with notes
		_, err = tx.Exec(`
			CREATE TRIGGER notes_after_insert AFTER INSERT ON notes BEGIN
				INSERT INTO note_fts(rowid, body) VALUES (new.rowid, new.body);
			END;
			CREATE TRIGGER notes_after_delete AFTER DELETE ON notes BEGIN
				INSERT INTO note_fts(note_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
			END;
			CREATE TRIGGER notes_after_update AFTER UPDATE ON notes BEGIN
				INSERT INTO note_fts(note_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
				INSERT INTO note_fts(rowid, body) VALUES (new.rowid, new.body);
			END;
		`)
		if err != nil {
			return errors.Wrap(err, "creating triggers for note_fts")
		}

		// populate fts indices
		_, err = tx.Exec(`INSERT INTO note_fts (rowid, body)
			SELECT rowid, body FROM notes;`)
		if err != nil {
			return errors.Wrap(err, "populating note_fts")
		}

		return nil
	},
}

var lm10 = migration{
	name: "rename-number-only-book",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		migrateBook := func(label string) error {
			var uuid string

			err := tx.QueryRow("SELECT uuid FROM books WHERE label = ?", label).Scan(&uuid)
			if err != nil {
				return errors.Wrap(err, "finding uuid")
			}

			for i := 1; ; i++ {
				candidate := fmt.Sprintf("%s (%d)", label, i)

				var count int
				err := tx.QueryRow("SELECT count(*) FROM books WHERE label = ?", candidate).Scan(&count)
				if err != nil {
					return errors.Wrap(err, "counting candidate")
				}

				if count == 0 {
					_, err := tx.Exec("UPDATE books SET label = ?, dirty = ? WHERE uuid = ?", candidate, true, uuid)
					if err != nil {
						return errors.Wrapf(err, "updating book '%s'", label)
					}

					break
				}
			}

			return nil
		}

		rows, err := tx.Query("SELECT label FROM books")
		defer rows.Close()
		if err != nil {
			return errors.Wrap(err, "getting labels")
		}

		var regexNumber = regexp.MustCompile(`^\d+$`)

		for rows.Next() {
			var label string
			err := rows.Scan(&label)
			if err != nil {
				return errors.Wrap(err, "scannign row")
			}

			if regexNumber.MatchString(label) {
				err = migrateBook(label)
				if err != nil {
					return errors.Wrapf(err, "migrating book %s", label)
				}
			}
		}

		return nil
	},
}

var lm11 = migration{
	name: "rename-book-labels-with-space",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		processLabel := func(label string) (string, error) {
			sanitized := strings.Replace(label, " ", "_", -1)

			var cnt int
			err := tx.QueryRow("SELECT count(*) FROM books WHERE label = ?", sanitized).Scan(&cnt)
			if err != nil {
				return "", errors.Wrap(err, "counting ret")
			}
			if cnt == 0 {
				return sanitized, nil
			}

			// if there is a collision, resolve by appending number
			var ret string
			for i := 2; ; i++ {
				ret = fmt.Sprintf("%s_%d", sanitized, i)

				var count int
				err := tx.QueryRow("SELECT count(*) FROM books WHERE label = ?", ret).Scan(&count)
				if err != nil {
					return "", errors.Wrap(err, "counting ret")
				}

				if count == 0 {
					break
				}
			}

			return ret, nil
		}

		rows, err := tx.Query("SELECT uuid, label FROM books")
		defer rows.Close()
		if err != nil {
			return errors.Wrap(err, "getting labels")
		}

		for rows.Next() {
			var uuid, label string
			err := rows.Scan(&uuid, &label)
			if err != nil {
				return errors.Wrap(err, "scanning row")
			}

			if strings.Contains(label, " ") {
				processed, err := processLabel(label)
				if err != nil {
					return errors.Wrapf(err, "resolving book name for %s", label)
				}

				_, err = tx.Exec("UPDATE books SET label = ?, dirty = ? WHERE uuid = ?", processed, true, uuid)
				if err != nil {
					return errors.Wrapf(err, "updating book '%s'", label)
				}
			}
		}

		return nil
	},
}

var lm12 = migration{
	name: "add apiEndpoint to the configuration file",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		// Obsolete: lflow no longer syncs, so the config has no apiEndpoint.
		// Kept as a no-op to preserve the schema version sequence of
		// already-migrated databases.
		return nil
	},
}

var lm13 = migration{
	name: "add enableUpgradeCheck to the configuration file",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		cf, err := config.Read(ctx)
		if err != nil {
			return errors.Wrap(err, "reading config")
		}

		cf.EnableUpgradeCheck = true

		err = config.Write(ctx, cf)
		if err != nil {
			return errors.Wrap(err, "writing config")
		}

		return nil
	},
}

var lm14 = migration{
	name: "drop-public-from-notes",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		_, err := tx.Exec(`ALTER TABLE notes DROP COLUMN public;`)
		if err != nil {
			return errors.Wrap(err, "dropping public column from notes")
		}

		return nil
	},
}

// lm15 converts the dnote book/note model into the lflow node outline model.
// Every book becomes a root node (type h1); every note becomes a child node
// whose name is the first line of the note body and whose note field holds the
// rest. Converted rows start over with usn=0/dirty=1 because the node-based
// server protocol is incompatible with old book/note USNs.
var lm15 = migration{
	name: "convert-books-notes-to-nodes",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS nodes
			(
				id integer PRIMARY KEY AUTOINCREMENT,
				uuid text NOT NULL UNIQUE,
				parent_uuid text NOT NULL DEFAULT '',
				rank integer NOT NULL DEFAULT 0,
				name text NOT NULL DEFAULT '',
				note text NOT NULL DEFAULT '',
				layout text NOT NULL DEFAULT 'bullets',
				mirror_of text NOT NULL DEFAULT '',
				completed_at integer NOT NULL DEFAULT 0,
				added_on integer NOT NULL DEFAULT 0,
				edited_on integer NOT NULL DEFAULT 0,
				usn integer NOT NULL DEFAULT 0,
				deleted bool NOT NULL DEFAULT false,
				dirty bool NOT NULL DEFAULT false
			)`)
		if err != nil {
			return errors.Wrap(err, "creating nodes table")
		}

		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_nodes_parent ON nodes(parent_uuid, rank);
			CREATE INDEX IF NOT EXISTS idx_nodes_dirty ON nodes(dirty);`)
		if err != nil {
			return errors.Wrap(err, "creating node indices")
		}

		_, err = tx.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS node_fts USING fts5(content=nodes, name, note, tokenize="porter unicode61 categories 'L* N* Co Ps Pe'");`)
		if err != nil {
			return errors.Wrap(err, "creating node_fts")
		}
		_, err = tx.Exec(`
			CREATE TRIGGER IF NOT EXISTS nodes_after_insert AFTER INSERT ON nodes BEGIN
				INSERT INTO node_fts(rowid, name, note) VALUES (new.id, new.name, new.note);
			END;
			CREATE TRIGGER IF NOT EXISTS nodes_after_delete AFTER DELETE ON nodes BEGIN
				INSERT INTO node_fts(node_fts, rowid, name, note) VALUES ('delete', old.id, old.name, old.note);
			END;
			CREATE TRIGGER IF NOT EXISTS nodes_after_update AFTER UPDATE ON nodes BEGIN
				INSERT INTO node_fts(node_fts, rowid, name, note) VALUES ('delete', old.id, old.name, old.note);
				INSERT INTO node_fts(rowid, name, note) VALUES (new.id, new.name, new.note);
			END;`)
		if err != nil {
			return errors.Wrap(err, "creating node_fts triggers")
		}

		// convert existing books/notes
		bookRows, err := tx.Query("SELECT uuid, label FROM books ORDER BY label")
		if err != nil {
			return errors.Wrap(err, "querying books")
		}
		defer bookRows.Close()

		type legacyBook struct{ uuid, label string }
		var books []legacyBook
		for bookRows.Next() {
			var b legacyBook
			if err := bookRows.Scan(&b.uuid, &b.label); err != nil {
				return errors.Wrap(err, "scanning a book")
			}
			books = append(books, b)
		}
		if err := bookRows.Err(); err != nil {
			return errors.Wrap(err, "iterating books")
		}

		now := time.Now().UnixNano()
		for rank, b := range books {
			if _, err := tx.Exec(`INSERT INTO nodes (uuid, parent_uuid, rank, name, layout, added_on, edited_on, usn, dirty)
				VALUES (?, '', ?, ?, 'h1', ?, ?, 0, 1)`, b.uuid, rank, b.label, now, now); err != nil {
				return errors.Wrapf(err, "converting book %s", b.label)
			}

			noteRows, err := tx.Query("SELECT uuid, content, added_on, edited_on FROM notes WHERE book_uuid = ? ORDER BY added_on", b.uuid)
			if err != nil {
				return errors.Wrapf(err, "querying notes of book %s", b.label)
			}

			type legacyNote struct {
				uuid, content     string
				addedOn, editedOn int64
			}
			var notes []legacyNote
			for noteRows.Next() {
				var n legacyNote
				if err := noteRows.Scan(&n.uuid, &n.content, &n.addedOn, &n.editedOn); err != nil {
					noteRows.Close()
					return errors.Wrap(err, "scanning a note")
				}
				notes = append(notes, n)
			}
			noteRows.Close()

			for noteRank, n := range notes {
				name := n.content
				note := ""
				if idx := strings.Index(n.content, "\n"); idx >= 0 {
					name = n.content[:idx]
					note = strings.TrimLeft(n.content[idx+1:], "\n")
				}
				if _, err := tx.Exec(`INSERT INTO nodes (uuid, parent_uuid, rank, name, note, layout, added_on, edited_on, usn, dirty)
					VALUES (?, ?, ?, ?, ?, 'bullets', ?, ?, 0, 1)`, n.uuid, b.uuid, noteRank, name, note, n.addedOn, n.editedOn); err != nil {
					return errors.Wrapf(err, "converting note %s", n.uuid)
				}
			}
		}

		// the node protocol starts over; old book/note usn state is void
		if _, err := tx.Exec("UPDATE system SET value = '0' WHERE key = ?", "last_max_usn"); err != nil {
			return errors.Wrap(err, "resetting last_max_usn")
		}

		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS notes_after_insert;
			DROP TRIGGER IF EXISTS notes_after_delete;
			DROP TRIGGER IF EXISTS notes_after_update;
			DROP TABLE IF EXISTS note_fts;
			DROP TABLE IF EXISTS notes;
			DROP TABLE IF EXISTS books;`); err != nil {
			return errors.Wrap(err, "dropping legacy tables")
		}

		return nil
	},
}

// lm16 adds the per-node style column that backs the /color, /bold, /italic
// and /underline editor commands. It holds a comma-separated token list, e.g.
// "bold,italic,color:blue"; empty means an unstyled node.
var lm16 = migration{
	name: "add-style-to-nodes",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`ALTER TABLE nodes ADD COLUMN style text NOT NULL DEFAULT '';`); err != nil {
			return errors.Wrap(err, "adding style column to nodes")
		}
		return nil
	},
}

// lm17 unwraps the legacy [[date]] pill brackets in node names. Dates are now
// recognised by format and chipped at render time, with no markers stored, so
// the old [[YYYY-MM-DD HH:MM]] tokens become bare dates. Only bracket pairs
// wrapping a canonical date are touched; any other [[ ]] text is left alone.
var lm17 = migration{
	name: "unwrap-date-pill-brackets",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		re := regexp.MustCompile(`\[\[(\d{4}-\d{2}-\d{2}(?: \d{2}:\d{2})?)\]\]`)

		rows, err := tx.Query("SELECT uuid, name FROM nodes WHERE name LIKE '%[[%'")
		if err != nil {
			return errors.Wrap(err, "querying nodes with brackets")
		}
		type update struct{ uuid, name string }
		var updates []update
		for rows.Next() {
			var uuid, name string
			if err := rows.Scan(&uuid, &name); err != nil {
				rows.Close()
				return errors.Wrap(err, "scanning node")
			}
			if stripped := re.ReplaceAllString(name, "$1"); stripped != name {
				updates = append(updates, update{uuid, stripped})
			}
		}
		rows.Close()

		for _, u := range updates {
			if _, err := tx.Exec("UPDATE nodes SET name = ? WHERE uuid = ?", u.name, u.uuid); err != nil {
				return errors.Wrapf(err, "unwrapping date pills in node %s", u.uuid)
			}
		}
		return nil
	},
}

// lm18 renames the layout column to type across the nodes table.
var lm18 = migration{
	name: "rename-layout-to-type",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`ALTER TABLE nodes RENAME COLUMN layout TO type`); err != nil {
			return errors.Wrap(err, "renaming layout column to type")
		}
		return nil
	},
}

// lm19 adds the per-node collapsed flag — local view-state (never synced) so the
// editor can restore each node's fold across restarts.
var lm19 = migration{
	name: "add-collapsed-to-nodes",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`ALTER TABLE nodes ADD COLUMN collapsed bool NOT NULL DEFAULT false;`); err != nil {
			return errors.Wrap(err, "adding collapsed column to nodes")
		}
		return nil
	},
}

// lm20 adds the per-node link_to: a single directed link to another node's uuid
// (rendered as → target, jumped to with alt+g). Persisted locally.
var lm20 = migration{
	name: "add-link-to-to-nodes",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`ALTER TABLE nodes ADD COLUMN link_to text NOT NULL DEFAULT '';`); err != nil {
			return errors.Wrap(err, "adding link_to column to nodes")
		}
		return nil
	},
}

// lm21 capitalizes the root node's name from the seeded lowercase "root" to
// "Root", so the breadcrumb reads "Root" without a render-time relabel. The root
// is local-only and never synced, so its dirty flag is left untouched.
var lm21 = migration{
	name: "capitalize-root-node-name",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec("UPDATE nodes SET name = 'Root' WHERE uuid = ? AND name = 'root'", database.RootUUID); err != nil {
			return errors.Wrap(err, "capitalizing root node name")
		}
		return nil
	},
}

// lm22 adds the per-node readonly flag — a node lock (e.g. a file node's path is
// locked once committed with Enter). Local-only like style/link_to: persisted so
// the lock survives a restart, but never synced.
var lm22 = migration{
	name: "add-readonly-to-nodes",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`ALTER TABLE nodes ADD COLUMN readonly bool NOT NULL DEFAULT false;`); err != nil {
			return errors.Wrap(err, "adding readonly column to nodes")
		}
		return nil
	},
}

// lm23 adds the node_output table — a bash/query node's captured run output,
// keyed by node uuid and decoupled from the node row's lifecycle, so output
// persists the instant a run finishes (before the node itself is saved). Local
// only, never synced: it replaces the old on-disk run cache with DB storage.
var lm23 = migration{
	name: "add-node-output-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS node_output (
			uuid text PRIMARY KEY,
			output text NOT NULL DEFAULT ''
		);`); err != nil {
			return errors.Wrap(err, "creating node_output table")
		}
		return nil
	},
}

// lm24 adds the chips table — inline structured tokens (path/date/tag) that a
// node's name references by an anchor. Local for now (path chips hold
// machine-specific absolute paths); cross-device sync is a later milestone.
var lm24 = migration{
	name: "add-chips-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS chips (
			id text PRIMARY KEY,
			kind text NOT NULL DEFAULT '',
			value text NOT NULL DEFAULT ''
		);`); err != nil {
			return errors.Wrap(err, "creating chips table")
		}
		return nil
	},
}

// lm25 removes the dnote sync bookkeeping. The usn/dirty node columns and the
// sync-only system keys tracked state for the (now removed) cross-device sync;
// the deleted tombstone column stays, as it drives local soft-delete.
var lm25 = migration{
	name: "drop-dnote-sync-state",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		// the dirty index must go before its column can be dropped
		if _, err := tx.Exec("DROP INDEX IF EXISTS idx_nodes_dirty"); err != nil {
			return errors.Wrap(err, "dropping idx_nodes_dirty")
		}
		if _, err := tx.Exec("ALTER TABLE nodes DROP COLUMN dirty"); err != nil {
			return errors.Wrap(err, "dropping nodes.dirty")
		}
		if _, err := tx.Exec("ALTER TABLE nodes DROP COLUMN usn"); err != nil {
			return errors.Wrap(err, "dropping nodes.usn")
		}
		if _, err := tx.Exec(`DELETE FROM system WHERE key IN
			('remote_schema', 'last_max_usn', 'last_sync_time', 'session_token', 'session_token_expiry')`); err != nil {
			return errors.Wrap(err, "deleting sync system keys")
		}
		return nil
	},
}

// lm26 removes the node→node/URL link feature: the link_to column held a single
// directed reference (rendered → target, followed with alt+g). The mirror
// feature (mirror_of) is unrelated and stays.
var lm26 = migration{
	name: "drop-link-to",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec("ALTER TABLE nodes DROP COLUMN link_to"); err != nil {
			return errors.Wrap(err, "dropping nodes.link_to")
		}
		return nil
	},
}

// lm27 adds the chips.label column. Path/date/tag chips leave it empty — their
// display derives from the value. A link chip uses it for the arbitrary display
// name, with the value holding the target (a URL or lflow://node/<uuid>).
var lm27 = migration{
	name: "add-chip-label",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec("ALTER TABLE chips ADD COLUMN label text NOT NULL DEFAULT ''"); err != nil {
			return errors.Wrap(err, "adding chips.label")
		}
		return nil
	},
}

// lm28 adds the settings table — global editor preferences (theme, link color, …)
// as key/value rows. Local UI state, kept out of the `system` table so app
// settings never mingle with the schema-version bookkeeping.
var lm28 = migration{
	name: "add-settings-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS settings (
			key text PRIMARY KEY,
			value text NOT NULL DEFAULT ''
		)`); err != nil {
			return errors.Wrap(err, "creating settings table")
		}
		return nil
	},
}

// lm29 adds the node_blobs table — an image node's pixels, stored as a PNG BLOB
// keyed by node uuid so the whole outline stays a single portable SQLite file. It
// is a separate table (not a nodes column) so the hot nodes scan and the FTS
// triggers are untouched by multi-KB blobs.
var lm29 = migration{
	name: "add-node-blobs-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS node_blobs (
			uuid text PRIMARY KEY,
			mime text NOT NULL DEFAULT '',
			bytes blob NOT NULL,
			w integer NOT NULL DEFAULT 0,
			h integer NOT NULL DEFAULT 0
		)`); err != nil {
			return errors.Wrap(err, "creating node_blobs table")
		}
		return nil
	},
}

// lm30 adds the artifacts table — runtime-loaded node-type plugins, one JS
// program per row (see AGENTS.md). Definitions live in the DB so they
// travel with the outline: installing an artifact is an INSERT, never a
// migration; this table is the last type-related schema change.
var lm30 = migration{
	name: "add-artifacts-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS artifacts (
			key text PRIMARY KEY,
			label text NOT NULL DEFAULT '',
			version integer NOT NULL DEFAULT 1,
			source text NOT NULL DEFAULT '',
			created_by text NOT NULL DEFAULT 'user',
			created_at integer NOT NULL DEFAULT 0,
			enabled bool NOT NULL DEFAULT true
		);`); err != nil {
			return errors.Wrap(err, "creating artifacts table")
		}
		return nil
	},
}

// lm31 seeds the "log" artifact — the one built-in node type that moved to the
// artifact model, doubling as the reference program agent-generated artifacts
// imitate. Its compiled-in registry entry and render branches are gone; the
// TypeLog constant stays for data compatibility.
var lm31 = migration{
	name: "seed-log-artifact",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`INSERT INTO artifacts (key, label, version, source, created_by, created_at, enabled)
			VALUES ('log', 'Log', 1, ?, 'seed', ?, true)
			ON CONFLICT(key) DO NOTHING`,
			database.SeedLogArtifactSource, time.Now().UnixNano()); err != nil {
			return errors.Wrap(err, "seeding log artifact")
		}
		return nil
	},
}

// lm32 adds the agent_sessions table — one row per @mention thread, binding a
// thread root node to a remote agent session id so a later mention in the same
// thread resumes the conversation (see pkg/tui/tag).
var lm32 = migration{
	name: "add-agent-sessions-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_sessions (
			id text PRIMARY KEY,
			node_uuid text NOT NULL DEFAULT '',
			agent text NOT NULL DEFAULT '',
			state text NOT NULL DEFAULT 'idle',
			created_at integer NOT NULL DEFAULT 0,
			updated_at integer NOT NULL DEFAULT 0
		);`); err != nil {
			return errors.Wrap(err, "creating agent_sessions table")
		}
		return nil
	},
}

// lm33 adds the wf_nodes table — the Workflowy mirror map. Each pulled node is
// bound to its Workflowy id so a re-pull reconciles in place instead of
// duplicating, and a future two-way sync can push edits back by id.
var lm33 = migration{
	name: "add-wf-nodes-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS wf_nodes (
			node_uuid text PRIMARY KEY,
			wf_id text NOT NULL DEFAULT '',
			synced_at integer NOT NULL DEFAULT 0
		);`); err != nil {
			return errors.Wrap(err, "creating wf_nodes table")
		}
		return nil
	},
}

// lm34 adds nodes.starred — the /star flag that pins a node to the top of the
// move/goto/mirror pickers. View-preference state like collapsed: toggled in
// place, no edited_on churn.
var lm34 = migration{
	name: "add-node-starred",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec("ALTER TABLE nodes ADD COLUMN starred bool NOT NULL DEFAULT false"); err != nil {
			return errors.Wrap(err, "adding nodes.starred")
		}
		return nil
	},
}

// lm35 adds the tag_colors table — manual per-tag colors (see editor/tagcolor.go).
// One row per tag word; no row = the default muted gray. Assigned via alt+e on
// a tag chip, rendered as a colored pill wherever the tag appears.
var lm35 = migration{
	name: "add-tag-colors-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS tag_colors (
			tag text PRIMARY KEY,
			color text NOT NULL DEFAULT ''
		);`); err != nil {
			return errors.Wrap(err, "creating tag_colors table")
		}
		return nil
	},
}

// lm36 adds the node_spans table — the painter's partial-text styling. One row
// per painted run (rune offsets into the node name), style = the same token
// vocabulary as nodes.style. The text itself stays markup-free, always.
var lm36 = migration{
	name: "add-node-spans-table",
	run: func(ctx context.DnoteCtx, tx *database.DB) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS node_spans (
			node_uuid text NOT NULL,
			start integer NOT NULL,
			end integer NOT NULL,
			style text NOT NULL DEFAULT '',
			PRIMARY KEY (node_uuid, start)
		);`); err != nil {
			return errors.Wrap(err, "creating node_spans table")
		}
		return nil
	},
}
