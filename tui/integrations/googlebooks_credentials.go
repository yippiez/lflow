// Google Books' block of the consolidated ~/.config/lflow/credentials.json.
package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// WARNING (invariant): credentials.json is local-only secret storage — its
// contents must never be written into the outline DB or synced anywhere. This
// is why the Google Books key has no /settings field the way the SearxNG URL
// does: a URL is configuration, a key is a secret, and the settings table is
// notebook state.

// booksCredentials is the googlebooks block of credentials.json:
//
//	{"googlebooks": {"api_key": "…"}}
//
// The key is OPTIONAL — the volumes endpoint answers anonymous queries. It only
// raises the per-IP quota, so a user who searches all day can name one.
type booksCredentials struct {
	GoogleBooks struct {
		APIKey string `json:"api_key"`
	} `json:"googlebooks"`
}

// LoadBooksAPIKey reads the Google Books key from
// <configDir>/lflow/credentials.json. A missing or malformed file returns "" —
// the search then simply goes out unauthenticated, which is the normal case.
func LoadBooksAPIKey(configDir string) string {
	b, err := os.ReadFile(filepath.Join(configDir, "lflow", "credentials.json"))
	if err != nil {
		return ""
	}
	var c booksCredentials
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return strings.TrimSpace(c.GoogleBooks.APIKey)
}
