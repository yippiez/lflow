package chips

import (
	"net/url"
	"strings"
)

// services a link can point at. A link chip whose target is a known service —
// the Google suite and the assistants — carries that service's unicode mark
// beside its name: "→▦ Q3 budget" instead of "→Q3 budget". The mark is the only
// difference; a service link keeps the same arrow, color, underline and gestures
// as every other link.
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
	// title derives a compact resource name from the URL path ("turkish-parl…"
	// for a HuggingFace dataset) — the "/<leaf>" that LinkName appends after the
	// key. Only services whose paths follow a fixed shape set it; "" for the
	// rest means the path carries nothing worth naming.
	title func(path string) string
}

// services is the ordered registry: ServiceFor returns the first entry whose
// host AND path prefix match (the docs.google.com family is told apart by path
// alone, so order is only a tiebreak). Adding a service is one entry here.
//
// This is a short list on purpose — a mark earns its place by being worth
// spotting in a wall of rows. docs.google.com paths that are not listed (a
// document, a form) stay ordinary links, which is exactly what an unrecognized
// target should look like.
var services = []Service{
	// docs.google.com hosts several apps and splits by path
	{Key: "sheets", Label: "Sheets", Glyph: "▦",
		hosts: []string{"docs.google.com", "sheets.google.com"}, path: "/spreadsheets"},
	{Key: "slides", Label: "Slides", Glyph: "▭",
		hosts: []string{"docs.google.com", "slides.google.com"}, path: "/presentation"},
	{Key: "drive", Label: "Drive", Glyph: "▲", hosts: []string{"drive.google.com"}},
	// Colab wears the infinity its own logo is built from
	{Key: "colab", Label: "Colab", Glyph: "∞",
		hosts: []string{"colab.research.google.com", "colab.google", "colab.sandbox.google.com"}},
	{Key: "gmail", Label: "Mail", Glyph: "✉", hosts: []string{"mail.google.com"}},
	// the assistants: a shared conversation is a link worth spotting, and each
	// takes the mark its own logo suggests — Claude's petal (the same ✽ the icon
	// catalog uses), Gemini's spark, a spoked star for ChatGPT
	{Key: "claude", Label: "Claude", Glyph: "✽", hosts: []string{"claude.ai", "claude.com"}},
	{Key: "gemini", Label: "Gemini", Glyph: "✦", hosts: []string{"gemini.google.com"}},
	{Key: "chatgpt", Label: "ChatGPT", Glyph: "✳", hosts: []string{"chatgpt.com", "chat.openai.com"}},
	// code-and-data hosts name their resource from the path — the hugging face
	// mark is the actual logo (there is no monochrome codepoint for it), and
	// GitHub, having no mark at all, stays name-only (see ServiceDisplay's guard)
	{Key: "huggingface", Label: "HuggingFace", Glyph: "🤗",
		hosts: []string{"huggingface.co", "hf.co"}, title: hfTitle},
	{Key: "github", Label: "GitHub", Glyph: "",
		hosts: []string{"github.com"}, title: ghTitle},
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
	for _, s := range services {
		if s.matches(host, u.Path) {
			return s, true
		}
	}
	return Service{}, false
}

// hfTitle is the HuggingFace path rule: /datasets/<owner>/<name> (and the
// models/spaces twins) or a bare /<owner>/<name> page — the leaf is the part
// worth keeping, so "alimetin/turkish-parliament-speech" names itself.
func hfTitle(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(parts) == 3 && (parts[0] == "datasets" || parts[0] == "models" || parts[0] == "spaces"):
		return parts[2]
	case len(parts) == 2:
		return parts[1]
	}
	return ""
}

// ghTitle is the GitHub path rule: /<owner>/<repo>, optionally with a path
// after it — the owner/repo pair is what identifies the resource.
func ghTitle(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

// LinkName is a link's default display name, most specific first: a service
// whose path names a resource gets "key/resource" ("huggingface/turkish-parlia-
// ment-speech"), a known service gets its own name ("Sheets"), and an
// unrecognized URL gets "" — the caller falls back to the host. Like
// ServiceFor, a scheme is optional.
func LinkName(target string) string {
	svc, ok := ServiceFor(target)
	if !ok {
		return ""
	}
	if svc.title != nil {
		if res := svc.title(pathOf(target)); res != "" {
			return svc.Key + "/" + res
		}
	}
	return svc.Label
}

// pathOf returns the parsed URL's path, "https://" added when the target has
// no scheme — the same canonicalization ServiceFor does, so the two agree.
func pathOf(target string) string {
	t := strings.TrimSpace(target)
	if !strings.Contains(t, "://") {
		t = "https://" + t
	}
	u, err := url.Parse(t)
	if err != nil {
		return ""
	}
	return u.Path
}

// serviceIconAfterTitle places the service mark: false puts it in FRONT of the
// title (right after the editor's "→"), true puts it after the title. One flag,
// one line, so the placement is a decision and not a scattering of + operators.
const serviceIconAfterTitle = false

// ServiceDisplay is a service link's display text: the chip's title, which falls
// back to the service's own name so a fresh link is never blank, plus the
// service's mark. A service with no mark (GitHub) simply omits it. Nothing else
// about the link changes — the editor still draws its "→" and the ordinary link
// styling around this.
func ServiceDisplay(s Service, label string) string {
	title := serviceTitle(s, label)
	if s.Glyph == "" {
		return title
	}
	if serviceIconAfterTitle {
		return title + " " + s.Glyph
	}
	return s.Glyph + " " + title
}

// serviceTitle is the chip's title — an arbitrary, user-renamed name, defaulting
// to the service's label.
func serviceTitle(s Service, label string) string {
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
