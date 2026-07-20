// Package webui embeds the built mobile web app (mobile/ → vite build) so
// `lflow serve --http` ships the client inside the binary. The dist tree here
// is a build artifact, committed so plain `go build` needs no npm toolchain;
// rebuild it with `npm run build` in mobile/ (its outDir points here).
package webui

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
