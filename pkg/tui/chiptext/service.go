package chiptext

import (
	"net/url"
	"strings"
)

// Services a link can point at. A link chip whose target is a known service —
// today the Google suite — carries that service's identity, so it renders with
// the service's unicode mark and title instead of the plain "→name" link (the
// editor also paints it in the service's color).
//
// The service is DERIVED from the target URL and never stored: no new chip kind,
// no column, no migration. A Google link made long before this file existed
// dresses itself the moment it is rendered, and retargeting a chip redresses it.
// This file is the colorless half — recognition, glyph, title — shared by the
// editor and the CLI; the colors live in pkg/tui/editor/service.go.

// Service is one recognized web service.
type Service struct {
	Key   string // stable key ("sheets"); the editor's color table keys on it
	Label string // human name, and the title of a chip that has none of its own
	Glyph string // the unicode mark drawn before the title

	hosts []string // the target's host must equal one of these (or be a subdomain)
	path  string   // path prefix the target must carry; "" matches any path
}

// Services is the ordered registry: ServiceFor returns the first entry whose
// host AND path prefix match (the docs.google.com family is told apart by path
// alone, so order is only a tiebreak). Adding a service is one entry here plus
// its color in the editor.
var Services = []Service{
	// the doc family shares one host and splits by path
	{Key: "docs", Label: "Docs", Glyph: "▤",
		hosts: []string{"docs.google.com"}, path: "/document"},
	{Key: "sheets", Label: "Sheets", Glyph: "▦",
		hosts: []string{"docs.google.com", "sheets.google.com"}, path: "/spreadsheets"},
	{Key: "slides", Label: "Slides", Glyph: "▭",
		hosts: []string{"docs.google.com", "slides.google.com"}, path: "/presentation"},
	{Key: "forms", Label: "Forms", Glyph: "☑",
		hosts: []string{"docs.google.com"}, path: "/forms"},
	{Key: "drive", Label: "Drive", Glyph: "▲", hosts: []string{"drive.google.com"}},
	{Key: "calendar", Label: "Calendar", Glyph: "◷", hosts: []string{"calendar.google.com"}},
	{Key: "gmail", Label: "Mail", Glyph: "✉", hosts: []string{"mail.google.com"}},
	{Key: "meet", Label: "Meet", Glyph: "▷", hosts: []string{"meet.google.com"}},
	// Google's own short link for a published form: no path, so the host is the
	// whole signal.
	{Key: "forms", Label: "Forms", Glyph: "☑", hosts: []string{"forms.gle"}},
}

// ServiceFor returns the service a link target belongs to, ok=false for any
// other URL. A scheme is optional — a pasted "docs.google.com/document/d/x" is
// recognized exactly like the https:// form, and the caller canonicalizes it.
func ServiceFor(target string) (Service, bool) {
	t := strings.TrimSpace(target)
	if t == "" {
		return Service{}, false
	}
	if !strings.Contains(t, "://") {
		t = "https://" + t
	}
	u, err := url.Parse(t)
	if err != nil || u.Hostname() == "" {
		return Service{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	for _, s := range Services {
		if s.matches(host, u.Path) {
			return s, true
		}
	}
	return Service{}, false
}

// ServiceDisplay is a service chip's colorless display: the glyph and the chip's
// title, which falls back to the service's own name so a fresh chip is never
// blank. Every surface (editor render, CLI list/grep) shows this same text; only
// the editor adds the brand color behind it.
func ServiceDisplay(s Service, label string) string {
	return s.Glyph + " " + ServiceTitle(s, label)
}

// ServiceTitle is the chip's title — an arbitrary, user-renamed name, defaulting
// to the service's label.
func ServiceTitle(s Service, label string) string {
	if strings.TrimSpace(label) == "" {
		return s.Label
	}
	return label
}

// matches reports whether a host/path pair belongs to this service.
func (s Service) matches(host, path string) bool {
	hit := false
	for _, h := range s.hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			hit = true
			break
		}
	}
	if !hit {
		return false
	}
	return s.path == "" || path == s.path || strings.HasPrefix(path, s.path+"/")
}
