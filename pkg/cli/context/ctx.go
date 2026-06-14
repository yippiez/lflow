// Package context defines lflow context
package context

import (
	"net/http"

	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/shared/clock"
)

// Paths contain directory definitions
type Paths struct {
	Home        string
	Config      string
	Data        string
	Cache       string
	LegacyDnote string
}

// DnoteCtx is a context holding the information of the current runtime
type DnoteCtx struct {
	Paths              Paths
	APIEndpoint        string
	Version            string
	DB                 *database.DB
	SessionKey         string
	SessionKeyExpiry   int64
	Editor             string
	Clock              clock.Clock
	EnableUpgradeCheck bool
	HTTPClient         *http.Client
}

// Redact replaces private information from the context with a set of
// placeholder values.
func Redact(ctx DnoteCtx) DnoteCtx {
	var sessionKey string
	if ctx.SessionKey != "" {
		sessionKey = "1"
	} else {
		sessionKey = "0"
	}
	ctx.SessionKey = sessionKey

	return ctx
}
