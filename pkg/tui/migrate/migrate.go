package migrate

import (
	"database/sql"

	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/log"
	"github.com/pkg/errors"
)

const (
	// LocalMode is a local migration mode
	LocalMode = iota
	// RemoteMode is a remote migration mode
	RemoteMode
)

// LocalSequence is a list of local migrations to be run
var LocalSequence = []migration{
	lm1,
	lm2,
	lm3,
	lm4,
	lm5,
	lm6,
	lm7,
	lm8,
	lm9,
	lm10,
	lm11,
	lm12,
	lm13,
	lm14,
	lm15,
	lm16,
	lm17,
	lm18,
	lm19,
	lm20,
	lm21,
	lm22,
	lm23,
	lm24,
	lm25,
	lm26,
}

// RemoteSequence is a list of remote migrations to be run
var RemoteSequence = []migration{
	rm1,
}

func initSchema(ctx context.DnoteCtx, schemaKey string) (int, error) {
	// schemaVersion is the index of the latest run migration in the sequence
	schemaVersion := 0

	db := ctx.DB
	_, err := db.Exec("INSERT INTO system (key, value) VALUES (?, ?)", schemaKey, schemaVersion)
	if err != nil {
		return schemaVersion, errors.Wrap(err, "inserting schema")
	}

	return schemaVersion, nil
}

func getSchemaKey(mode int) (string, error) {
	if mode == LocalMode {
		return consts.SystemSchema, nil
	}

	if mode == RemoteMode {
		return consts.SystemRemoteSchema, nil
	}

	return "", errors.Errorf("unsupported migration type '%d'", mode)
}

func getSchema(ctx context.DnoteCtx, schemaKey string) (int, error) {
	var ret int

	db := ctx.DB
	err := db.QueryRow("SELECT value FROM system where key = ?", schemaKey).Scan(&ret)
	if err == sql.ErrNoRows {
		ret, err = initSchema(ctx, schemaKey)

		if err != nil {
			return ret, errors.Wrap(err, "initializing schema")
		}
	} else if err != nil {
		return ret, errors.Wrap(err, "querying schema")
	}

	return ret, nil
}

func execute(ctx context.DnoteCtx, m migration, schemaKey string) error {
	log.Debug("running migration %s\n", m.name)

	tx, err := ctx.DB.Begin()
	if err != nil {
		return errors.Wrap(err, "beginning a transaction")
	}

	err = m.run(ctx, tx)
	if err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "running '%s'", m.name)
	}

	var currentSchema int
	err = tx.QueryRow("SELECT value FROM system WHERE key = ?", schemaKey).Scan(&currentSchema)
	if err != nil {
		tx.Rollback()
		return errors.Wrap(err, "getting current schema")
	}

	_, err = tx.Exec("UPDATE system SET value = value + 1 WHERE key = ?", schemaKey)
	if err != nil {
		tx.Rollback()
		return errors.Wrap(err, "incrementing schema")
	}

	tx.Commit()

	return nil
}

// Run performs unrun migrations
func Run(ctx context.DnoteCtx, migrations []migration, mode int) error {
	schemaKey, err := getSchemaKey(mode)
	if err != nil {
		return errors.Wrap(err, "getting schema key")
	}

	schema, err := getSchema(ctx, schemaKey)
	if err != nil {
		return errors.Wrap(err, "getting the current schema")
	}

	log.Debug("%s: %d of %d\n", schemaKey, schema, len(migrations))

	toRun := migrations[schema:]

	for _, m := range toRun {
		if err := execute(ctx, m, schemaKey); err != nil {
			return errors.Wrap(err, "running migration")
		}
	}

	return nil
}
