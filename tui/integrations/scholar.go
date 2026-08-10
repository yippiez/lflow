// Google Scholar, asked the only way lflow asks anything of the web: through
// the user's own SearxNG instance (searxng.go), narrowed to its google_scholar
// engine. Scholar itself has no API and answers a scraper with a captcha, so
// the instance is not a convenience here — it is the whole mechanism, and the
// one setup step is on its side: google_scholar must be enabled in its
// settings.yml (it ships enabled in SearxNG's default engine list, but a
// trimmed instance may not have it). SearchScholar names that explicitly when
// an otherwise healthy instance answers with nothing.
//
// A scholar hit is a paper, not a page, so it carries what a paper has and a
// web hit does not: authors, journal, year. Engines disagree about how they
// write those fields — a list of authors here, one string there, a null where
// a date was expected — so the fields are decoded leniently: a shape nobody
// expected costs that one field, never the whole answer.
//
// See the package doc (integrations.go) for the shared credential invariant.

package integrations

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

// ScholarEngine is the SearxNG engine name for Google Scholar — the one engine
// a scholar search asks, so the hits are papers and nothing else.
const ScholarEngine = "google_scholar"

// scholarAuthorCap is how many authors a citation names before "et al." — a
// row is a glance, and a physics paper's author list is not one.
const scholarAuthorCap = 3

// ScholarResult is one Google Scholar hit: a web result plus the citation
// fields that make it a paper. Every field beyond Title and URL is
// best-effort — Scholar's own green line is what the engine parses, and it is
// prose, not a record.
type ScholarResult struct {
	Title   string
	URL     string
	Snippet string
	Authors []string
	Journal string
	Year    string
}

// Citation renders the paper's provenance as one plain line —
// "Knuth, Bentley · Communications of the ACM · 1974" — skipping whatever the
// engine did not give us. Empty when nothing is known, which a caller reads as
// "show the title alone" rather than printing a lonely separator.
func (r ScholarResult) Citation() string {
	var parts []string
	if a := r.authorList(); a != "" {
		parts = append(parts, a)
	}
	if r.Journal != "" {
		parts = append(parts, r.Journal)
	}
	if r.Year != "" {
		parts = append(parts, r.Year)
	}
	return strings.Join(parts, " · ")
}

// authorList names up to scholarAuthorCap authors and abbreviates the rest.
func (r ScholarResult) authorList() string {
	names := make([]string, 0, len(r.Authors))
	for _, a := range r.Authors {
		if a = cleanText(a); a != "" {
			names = append(names, a)
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) > scholarAuthorCap {
		return strings.Join(names[:scholarAuthorCap], ", ") + " et al."
	}
	return strings.Join(names, ", ")
}

// SearchScholar returns at most limit papers for query (limit <= 0 means
// DefaultLimit). It is Search's sibling: same instance, same resolution order,
// same errors — narrowed to the google_scholar engine.
func (c *Client) SearchScholar(ctx context.Context, query string, limit int) ([]ScholarResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty search")
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	endpoint, configured := c.instance()
	body, err := c.fetch(ctx, endpoint, query, ScholarEngine)
	if err != nil {
		if !configured && ctx.Err() == nil {
			// nothing was named and nothing answered on the local port: the user has
			// no instance, which is a setup message, not a search failure
			return nil, errNoInstance
		}
		return nil, err
	}
	hits, err := ParseScholar(body, limit)
	if err != nil {
		return nil, errors.Wrapf(err, "reading %s", hostOf(endpoint))
	}
	if len(hits) == 0 {
		// a reachable instance that answers an ordinary query with nothing is the
		// engine being off far more often than Scholar having no paper on it
		return nil, errors.Errorf("no scholar results from %s — enable the %s engine on the instance",
			hostOf(endpoint), ScholarEngine)
	}
	return hits, nil
}

// scholarResponse is the slice of SearxNG's JSON answer a scholar hit needs.
// The citation fields stay raw: see the file doc — engines write them in more
// than one shape, and each is decoded on its own so an odd one cannot fail the
// whole document.
type scholarResponse struct {
	Results []struct {
		Title         string          `json:"title"`
		URL           string          `json:"url"`
		Content       string          `json:"content"`
		Authors       json.RawMessage `json:"authors"`
		Journal       json.RawMessage `json:"journal"`
		Publisher     json.RawMessage `json:"publisher"`
		PublishedDate json.RawMessage `json:"publishedDate"`
	} `json:"results"`
}

// ParseScholar reads a SearxNG JSON answer as papers, keeping the instance's
// own ranking. It is exported so the JSON contract is testable without an
// instance.
func ParseScholar(body string, limit int) ([]ScholarResult, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	var resp scholarResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, errors.Wrap(err, "decoding the searxng answer")
	}
	var out []ScholarResult
	seen := map[string]bool{}
	for _, r := range resp.Results {
		if len(out) >= limit {
			break
		}
		link, title := resolveURL(r.URL), cleanText(r.Title)
		if link == "" || title == "" || seen[link] {
			continue
		}
		seen[link] = true
		// journal is where a paper appeared and publisher is who put it there;
		// Scholar's green line often carries only one of the two, so the citation
		// shows whichever came back
		venue := jsonString(r.Journal)
		if venue == "" {
			venue = jsonString(r.Publisher)
		}
		out = append(out, ScholarResult{
			Title:   title,
			URL:     link,
			Snippet: cleanText(r.Content),
			Authors: jsonStrings(r.Authors),
			Journal: cleanText(venue),
			Year:    jsonYear(r.PublishedDate),
		})
	}
	return out, nil
}

// jsonString reads a field an engine writes as a string, tolerating null and
// any other shape (→ "").
func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// jsonStrings reads a field an engine writes either as a list of strings or as
// one comma-separated string, tolerating null and any other shape (→ nil).
func jsonStrings(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	if s := jsonString(raw); s != "" {
		return strings.Split(s, ",")
	}
	return nil
}

// yearRe matches a plausible publication year anywhere in a date field, so an
// ISO timestamp, a bare year and a written date all yield the same thing.
var yearRe = regexp.MustCompile(`\b(1[5-9]\d{2}|20\d{2}|21\d{2})\b`)

// jsonYear pulls the year out of a publishedDate field. SearxNG serializes a
// parsed date as an ISO timestamp and leaves an unparsed one as whatever the
// engine wrote (or null), so the year is read out of the raw text rather than
// decoded as a date — a citation wants "1974", not a clock.
func jsonYear(raw json.RawMessage) string {
	s := jsonString(raw)
	if s == "" {
		s = string(raw) // an engine that wrote a bare number rather than a string
	}
	return yearRe.FindString(s)
}
