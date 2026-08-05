// Zotero's block of the consolidated ~/.config/lflow/credentials.json.
package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WARNING (invariant): credentials.json is local-only secret storage — its
// contents must never be written into the outline DB or synced anywhere.

// zoteroCredentials is the zotero block of ~/.config/lflow/credentials.json:
//
//	{"zotero": {"username": "…"}}
//
// Only the account name is read. It is a fallback for the web-library URL: a
// synced Zotero already records its username in the library itself, so this is
// for a library that has never signed in locally.
type zoteroCredentials struct {
	Zotero struct {
		Username string `json:"username"`
	} `json:"zotero"`
}

// LoadUsername reads the zotero.org account name from
// <configDir>/lflow/credentials.json. A missing or malformed file returns "" —
// the chip then explains how to set it instead of failing cryptically.
func LoadUsername(configDir string) string {
	b, err := os.ReadFile(filepath.Join(configDir, "lflow", "credentials.json"))
	if err != nil {
		return ""
	}
	var c zoteroCredentials
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return c.Zotero.Username
}
