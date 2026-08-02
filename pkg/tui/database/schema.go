package database

import (
	_ "embed"
)

//go:embed schema.sql
var schemaSQL string

// DefaultSchemaSQL is the canonical lflow schema: the full final state of the
// database, applied directly when a fresh database is created. lflow has no
// migrations — this file is the schema, hand-maintained.
func DefaultSchemaSQL() string {
	return schemaSQL
}
