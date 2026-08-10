// Internet Archive: the archive node asks archive.org's advanced-search API for
// ITEMS — the scanned books, concert recordings, films, magazines and software
// of the archive's own collections. Deliberately NOT the Wayback Machine: a
// wayback lookup answers "what did this URL look like in 2011", which needs a
// URL and returns snapshots of a page the user already knows about, while a
// search node starts from words and wants things it does not know about yet.
// So the backend here is advancedsearch.php, whose docs are archive.org items,
// and each hit's link is that item's /details/ page.
//
// Nothing is configured and nothing is stored: archive.org is one public host
// that needs no account and no API key, which is why this client — unlike the
// SearxNG one (searxng.go) — has no credentials block and no /settings field.
// The Endpoint field exists so tests can point it at a local server.
//
// See the package doc (integrations.go) for the shared credential invariant.

package integrations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// archiveEndpoint is archive.org's advanced-search API — the JSON face of the
// same search box the site's own header uses.
const archiveEndpoint = "https://archive.org/advancedsearch.php"

// archiveDetails is where one item lives: an identifier's human page, which is
// what a hit row links to.
const archiveDetails = "https://archive.org/details/"

// archiveFields are the item fields a hit needs: what to call it, who made it,
// when, what kind of thing it is, and the identifier the link is built from.
var archiveFields = []string{"identifier", "title", "creator", "year", "mediatype", "description"}

// ArchiveClient searches archive.org's collections. The zero value is usable —
// there is nothing to configure; Endpoint/HTTP exist so tests point the client
// at a local server.
type ArchiveClient struct {
	Endpoint string
	HTTP     *http.Client
}

func (c *ArchiveClient) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return archiveEndpoint
}

func (c *ArchiveClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// Search returns at most limit archive.org items for query (limit <= 0 means
// DefaultLimit).
func (c *ArchiveClient) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty search")
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	body, err := c.fetch(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	hits, err := ParseArchiveOrg(body, limit)
	if err != nil {
		return nil, errors.Wrapf(err, "reading %s", hostOf(c.endpoint()))
	}
	if len(hits) == 0 {
		return nil, errors.Errorf("no results from %s", hostOf(c.endpoint()))
	}
	return hits, nil
}

// fetch GETs the query from the advanced-search API and asks for its JSON
// output. The field list is explicit so the answer stays small: without fl[]
// the API returns every indexed field of every item.
func (c *ArchiveClient) fetch(ctx context.Context, query string, limit int) (string, error) {
	form := url.Values{
		"q":      {query},
		"fl[]":   archiveFields,
		"rows":   {strconv.Itoa(limit)},
		"page":   {"1"},
		"output": {"json"},
	}
	endpoint := c.endpoint() + "?" + form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", errors.Wrap(err, "building the archive.org request")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", errors.Wrapf(err, "calling %s", hostOf(endpoint))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", errors.Wrapf(err, "reading %s", hostOf(endpoint))
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf("%s: %s", hostOf(endpoint), resp.Status)
	}
	return string(body), nil
}

// archiveField is one metadata field of an item. archive.org's index is
// multi-valued: an item with two authors answers with a LIST where another item
// answers with a string, and the same field flips shape between hits of one
// search. Both readings land here as strings, and anything else (a number in
// year, say) keeps its literal text rather than failing the whole decode.
type archiveField []string

func (f *archiveField) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*f = archiveField{one}
		return nil
	}
	var many []string
	if json.Unmarshal(b, &many) == nil {
		*f = archiveField(many)
		return nil
	}
	*f = archiveField{strings.Trim(string(b), `"`)}
	return nil
}

// first is the field's leading value, or "".
func (f archiveField) first() string {
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// archiveResponse is the slice of the advanced-search answer the node needs.
type archiveResponse struct {
	Response struct {
		Docs []struct {
			Identifier  archiveField `json:"identifier"`
			Title       archiveField `json:"title"`
			Creator     archiveField `json:"creator"`
			Year        archiveField `json:"year"`
			Mediatype   archiveField `json:"mediatype"`
			Description archiveField `json:"description"`
		} `json:"docs"`
	} `json:"response"`
}

// ParseArchiveOrg reads an advanced-search JSON answer, keeping archive.org's
// own ranking. It is exported so the JSON contract is testable without the
// network.
func ParseArchiveOrg(body string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	var resp archiveResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, errors.Wrap(err, "decoding the archive.org answer")
	}
	var out []Result
	seen := map[string]bool{}
	for _, d := range resp.Response.Docs {
		if len(out) >= limit {
			break
		}
		id := cleanText(d.Identifier.first())
		if id == "" {
			continue
		}
		link := resolveURL(archiveDetails + url.PathEscape(id))
		if link == "" || seen[link] {
			continue
		}
		// an untitled item still deserves a row: its identifier IS its name on
		// archive.org, and dropping the hit would hide a real result
		title := cleanText(d.Title.first())
		if title == "" {
			title = id
		}
		seen[link] = true
		out = append(out, Result{Title: title, URL: link, Snippet: archiveSnippet(d.Creator, d.Year, d.Mediatype, d.Description)})
	}
	return out, nil
}

// archiveSnippet says what the item IS in one line — who made it, when, and of
// what kind — falling back to the item's description when it has no such
// metadata. An archive hit is a thing rather than a page, so the provenance is
// what a reader wants beside the title.
func archiveSnippet(creator, year, mediatype, description archiveField) string {
	var parts []string
	for _, f := range []archiveField{creator, year, mediatype} {
		if v := cleanText(f.first()); v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return cleanText(description.first())
	}
	return strings.Join(parts, " · ")
}
