// Workflowy's block of the consolidated ~/.config/lflow/credentials.json.
package integrations

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WARNING (invariant): credentials.json is local-only secret storage — its
// contents must never be written into the outline DB or synced anywhere.

// workflowyCredentials is the consolidated ~/.config/lflow/credentials.json
// shape. Only the workflowy block exists today; future services add sibling
// keys.
type workflowyCredentials struct {
	Workflowy struct {
		APIKey string `json:"api_key"`
	} `json:"workflowy"`
}

// LoadAPIKey reads the Workflowy API key from <configDir>/lflow/credentials.json.
// A missing or malformed file returns "" — the wf node then reports how to set
// the key up instead of failing cryptically.
func LoadAPIKey(configDir string) string {
	b, err := os.ReadFile(filepath.Join(configDir, "lflow", "credentials.json"))
	if err != nil {
		return ""
	}
	var c workflowyCredentials
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return c.Workflowy.APIKey
}
