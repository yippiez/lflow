// Package mobile is the lflow client for everything that is not a terminal:
// a web app (React + TS + Vite, source in web/) that talks to the daemon's
// HTTP API. The built app is embedded here so `lflow serve --http`
// ships the client inside the binary and a plain `go build` needs no npm
// toolchain — dist/ is a committed build artifact, rebuilt with `npm run build`
// in web/ (its outDir points at it). Capacitor wraps the same build for Android.
//
// This package is a leaf: it holds the client and nothing else, so the daemon
// can serve it without either side knowing about the other's internals.
package mobile

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the app with an SPA fallback: unknown paths get index.html
// so client-side routes survive a reload.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := sub.Open(p); err == nil {
				f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
