// Package context defines lflow context
package context

import (
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/utils/clock"
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
	Version            string
	DB                 *database.DB
	Editor             string
	Clock              clock.Clock
	EnableUpgradeCheck bool
}
