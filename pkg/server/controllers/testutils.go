package controllers

import (
	"net/http/httptest"
	"testing"

	"github.com/lflow/lflow/pkg/server/app"
	"github.com/pkg/errors"
)

// MustNewServer is a test utility function to initialize a new server
// with the given app
func MustNewServer(t *testing.T, a *app.App) *httptest.Server {
	server, err := NewServer(a)
	if err != nil {
		t.Fatal(errors.Wrap(err, "initializing router"))
	}

	return server
}

func NewServer(a *app.App) (*httptest.Server, error) {
	ctl := New(a)
	rc := RouteConfig{
		WebRoutes:   NewWebRoutes(a, ctl),
		APIRoutes:   NewAPIRoutes(a, ctl),
		Controllers: ctl,
	}
	r, err := NewRouter(a, rc)
	if err != nil {
		return nil, errors.Wrap(err, "initializing router")
	}

	server := httptest.NewServer(r)

	return server, nil
}
