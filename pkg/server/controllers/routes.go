package controllers

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lflow/lflow/pkg/server/app"
	"github.com/lflow/lflow/pkg/server/assets"
	mw "github.com/lflow/lflow/pkg/server/middleware"
	"github.com/pkg/errors"
)

// Route represents a single route
type Route struct {
	Method    string
	Pattern   string
	Handler   http.HandlerFunc
	RateLimit bool
}

// RouteConfig is the configuration for routes
type RouteConfig struct {
	Controllers *Controllers
	WebRoutes   []Route
	APIRoutes   []Route
}

// NewWebRoutes returns a new web routes
func NewWebRoutes(a *app.App, c *Controllers) []Route {
	redirectGuest := &mw.AuthParams{RedirectGuestsToLogin: true}

	ret := []Route{
		{"GET", "/", mw.Auth(a.DB, c.Users.Settings, redirectGuest), true},
		{"GET", "/about", mw.Auth(a.DB, c.Users.About, redirectGuest), true},
		{"GET", "/login", mw.GuestOnly(a.DB, c.Users.NewLogin), true},
		{"POST", "/login", mw.GuestOnly(a.DB, c.Users.Login), true},
		{"POST", "/logout", c.Users.Logout, true},

		{"GET", "/password-reset", c.Users.PasswordResetView.ServeHTTP, true},
		{"PATCH", "/password-reset", c.Users.PasswordReset, true},
		{"GET", "/password-reset/{token}", c.Users.PasswordResetConfirm, true},
		{"POST", "/reset-token", c.Users.CreateResetToken, true},
		{"PATCH", "/account/profile", mw.Auth(a.DB, c.Users.ProfileUpdate, nil), true},
		{"PATCH", "/account/password", mw.Auth(a.DB, c.Users.PasswordUpdate, nil), true},

		{"GET", "/health", c.Health.Index, true},
	}

	if !a.DisableRegistration {
		ret = append(ret, Route{"GET", "/join", c.Users.New, true})
		ret = append(ret, Route{"POST", "/join", c.Users.Create, true})
	}

	return ret
}

// NewAPIRoutes returns a new api routes
func NewAPIRoutes(a *app.App, c *Controllers) []Route {
	return []Route{
		// v3
		{"GET", "/v3/sync/fragment", mw.Auth(a.DB, c.Sync.GetSyncFragment, nil), false},
		{"GET", "/v3/sync/state", mw.Auth(a.DB, c.Sync.GetSyncState, nil), false},
		{"POST", "/v3/signin", c.Users.V3Login, true},
		{"POST", "/v3/signout", c.Users.V3Logout, true},
		{"OPTIONS", "/v3/signout", c.Users.logoutOptions, true},
		{"GET", "/v3/nodes", mw.Auth(a.DB, c.Nodes.V3Index, nil), true},
		{"GET", "/v3/nodes/{nodeUUID}", mw.Auth(a.DB, c.Nodes.V3Show, nil), true},
		{"POST", "/v3/nodes", mw.Auth(a.DB, c.Nodes.V3Create, nil), true},
		{"DELETE", "/v3/nodes/{nodeUUID}", mw.Auth(a.DB, c.Nodes.V3Delete, nil), true},
		{"PATCH", "/v3/nodes/{nodeUUID}", mw.Auth(a.DB, c.Nodes.V3Update, nil), true},
		{"OPTIONS", "/v3/nodes", c.Nodes.IndexOptions, true},
	}
}

func registerRoutes(router *mux.Router, wrapper mw.Middleware, app *app.App, routes []Route) {
	for _, route := range routes {
		wrappedHandler := wrapper(route.Handler, app, route.RateLimit)

		router.
			Handle(route.Pattern, wrappedHandler).
			Methods(route.Method)
	}
}

// NewRouter creates and returns a new router
func NewRouter(app *app.App, rc RouteConfig) (http.Handler, error) {
	if err := app.Validate(); err != nil {
		return nil, errors.Wrap(err, "validating the app parameters")
	}

	router := mux.NewRouter().StrictSlash(true)

	webRouter := router.PathPrefix("/").Subrouter()
	apiRouter := router.PathPrefix("/api").Subrouter()
	registerRoutes(webRouter, mw.WebMw, app, rc.WebRoutes)
	registerRoutes(apiRouter, mw.APIMw, app, rc.APIRoutes)

	router.PathPrefix("/api/v1").Handler(mw.ApplyLimit(mw.NotSupported, true))
	router.PathPrefix("/api/v2").Handler(mw.ApplyLimit(mw.NotSupported, true))

	// static
	staticFs, err := assets.GetStaticFS()
	if err != nil {
		return nil, errors.Wrap(err, "getting the filesystem for static files")
	}

	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticFs)))
	router.PathPrefix("/static/").Handler(staticHandler)

	router.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("User-agent: *\nAllow: /"))
	})

	// catch-all
	router.PathPrefix("/").HandlerFunc(rc.Controllers.Static.NotFound)

	return mw.Global(router), nil
}
