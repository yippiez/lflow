// Package context defines lflow context
package context

import (
	"github.com/lflow/lflow/pkg/tui/client"
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
	Paths   Paths
	Version string
	// DB is the outline database. In normal runs its driver speaks the wire
	// protocol to the daemon (the single process owning the SQLite file);
	// with LFLOW_NO_DAEMON=1 — and in tests — it is a direct file handle.
	DB *database.DB
	// Live is the daemon connection behind DB: the change-feed subscription
	// the editor uses for live updates. nil in direct (daemon-less) runs.
	Live               *client.Client
	Editor             string
	Clock              clock.Clock
	EnableUpgradeCheck bool
}
