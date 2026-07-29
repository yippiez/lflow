package chiptext

import (
	"net/url"
	"strings"
)

// Services a link can point at. A link chip whose target is a known service —
// today the Google suite — carries that service's unicode mark beside its name:
// "→▦ Q3 budget" instead of "→Q3 budget". The mark is the only difference; a
// service link keeps the same arrow, color, underline and gestures as every
// other link.
//
// The service is DERIVED from the target URL and never stored: no new chip kind,
// no column, no migration. A Google link made long before this file existed
// picks up its mark the moment it is rendered, and retargeting a chip remarks
// it. Recognition lives here, beside the other shared chip vocabulary, so the
// editor and the CLI show the same thing.

// Service is one recognized web service.
type Service struct {
	Key   string // stable key ("sheets")
	Label string // human name, and the title of a chip that has none of its own
	Glyph string // the unicode mark shown beside the title

	hosts []string // the target's host must equal one of these (or be a subdomain)
	path  string   // path prefix the target must carry; "" matches any path
}

// Services is the ordered registry: ServiceFor returns the first entry whose
// host AND path prefix match (the docs.google.com family is told apart by path
// alone, so order is only a tiebreak). Adding a service is one entry here.
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
	// Colab wears the infinity its own logo is built from, Gemini the spark
	{Key: "colab", Label: "Colab", Glyph: "∞",
		hosts: []string{"colab.research.google.com", "colab.google", "colab.sandbox.google.com"}},
	{Key: "gemini", Label: "Gemini", Glyph: "✦", hosts: []string{"gemini.google.com"}},
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

// serviceIconAfterTitle places the service mark: false puts it in FRONT of the
// title (right after the editor's "→"), true puts it after the title. One flag,
// one line, so the placement is a decision and not a scattering of + operators.
const serviceIconAfterTitle = false

// ServiceDisplay is a service link's display text: the chip's title, which falls
// back to the service's own name so a fresh link is never blank, plus the
// service's mark. Nothing else about the link changes — the editor still draws
// its "→" and the ordinary link styling around this.
func ServiceDisplay(s Service, label string) string {
	title := ServiceTitle(s, label)
	if serviceIconAfterTitle {
		return title + " " + s.Glyph
	}
	return s.Glyph + " " + title
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
