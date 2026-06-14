package database

import (
	"database/sql"
)

// NullString is a string that can be null
type NullString struct {
	sql.NullString
}

// ToNullString returns a NullString with given string
func ToNullString(v string) NullString {
	return NullString{
		sql.NullString{
			String: v,
			Valid:  true,
		},
	}
}
