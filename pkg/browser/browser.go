// Package browser opens web URLs in the user's default browser and tells a URL
// apart from other text — used by the editor's /link so a node can link to a
// website, not just another node.
package browser

import (
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// schemeRe matches an explicit URL scheme prefix like "https://" or "ftp://".
var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*://`)

// IsURL reports whether s looks like a web address — an explicit scheme
// (https://…) or a bare "www." host — so /link can route it to the browser
// instead of the node finder. A node UUID (hex, no scheme) is never a URL.
func IsURL(s string) bool {
	s = strings.TrimSpace(s)
	return schemeRe.MatchString(s) || strings.HasPrefix(s, "www.")
}

// Normalize canonicalizes a user-typed URL: a bare "www.example.com" gets an
// "https://" so the OS opener receives a well-formed address. A URL that already
// carries a scheme is returned unchanged.
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	if !schemeRe.MatchString(s) && strings.HasPrefix(s, "www.") {
		return "https://" + s
	}
	return s
}

// Open launches the user's default browser at url, detached — it returns once
// the opener is started and never takes over the terminal (a browser is a GUI
// app, so there is nothing to suspend the inline UI for).
//
// macOS uses "open" and Windows rundll32. On Linux it tries xdg-open, then
// wslview, then hands off to Windows' shell via cmd.exe — so it works on a bare
// WSL install where xdg-open is absent and the browser lives on the host.
func Open(url string) error {
	url = Normalize(url)
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	for _, opener := range []string{"xdg-open", "wslview"} {
		if path, err := exec.LookPath(opener); err == nil {
			return exec.Command(path, url).Start()
		}
	}
	// WSL with no Linux opener: let Windows' shell handle it (start's first quoted
	// arg is the window title, so pass an empty one before the url).
	if path, err := exec.LookPath("cmd.exe"); err == nil {
		return exec.Command(path, "/c", "start", "", url).Start()
	}
	return exec.Command("xdg-open", url).Start() // nothing found: surface the canonical error
}
