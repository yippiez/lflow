package controllers

import (
	"github.com/lflow/lflow/pkg/server/app"
	"github.com/lflow/lflow/pkg/server/views"
)

// Controllers is a group of controllers
type Controllers struct {
	Users  *Users
	Nodes  *Nodes
	Sync   *Sync
	Static *Static
	Health *Health
}

// New returns a new group of controllers
func New(app *app.App) *Controllers {
	c := Controllers{}

	viewEngine := views.NewDefaultEngine()

	c.Users = NewUsers(app, viewEngine)
	c.Nodes = NewNodes(app)
	c.Sync = NewSync(app)
	c.Static = NewStatic(app, viewEngine)
	c.Health = NewHealth(app)

	return &c
}
