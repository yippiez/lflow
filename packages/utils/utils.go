package utils

import (
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/pkg/errors"
)

// GenerateUUID returns a uuid v4 in string
func GenerateUUID() (string, error) {
	u, err := uuid.NewRandom()
	if err != nil {
		return "", errors.Wrap(err, "generating uuid")
	}

	return u.String(), nil
}

// WordBound reports whether the byte range [start,end) of s sits on its own:
// not glued to a letter or digit on either side.
func WordBound(s string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:start])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// Atoi parses an int, returning 0 on error.
func Atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
