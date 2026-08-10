// Google Books: the books node hands its name to the Volumes API and gets back
// citable volumes — title, byline, year, publisher, ISBN and the book's page on
// books.google.com. No configuration is needed: the volumes endpoint answers
// anonymous queries, which is the whole point of the node — a book search that
// works the moment the type is chosen. An API key is optional and only raises
// the per-IP quota:
//
//	~/.config/lflow/credentials.json  {"googlebooks": {"api_key": "…"}}
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

// booksEndpoint is Google's public volumes search. It is not configurable:
// unlike SearxNG — which is the user's OWN metasearch and therefore has to be
// named — there is exactly one Google Books, so a URL field would only be a way
// to point the node at something that is not it. Tests override it per client.
const booksEndpoint = "https://www.googleapis.com/books/v1/volumes"

// BooksLimit is how many volumes a search keeps. Google's own maxResults caps
// at 40; ten is one screenful, matching the web node.
const BooksLimit = 10

// booksMaxResults is the ceiling the API itself enforces on maxResults.
const booksMaxResults = 40

const booksUserAgent = "lflow/books"

// Book is one volume from a search.
type Book struct {
	Title     string
	Subtitle  string
	Authors   []string
	Publisher string
	// Year is the four-digit publication year, "" when the volume carries no
	// usable date (Google's publishedDate is anything from "2004" to "2004-05-01").
	Year string
	// ISBN is the volume's ISBN-13, falling back to its ISBN-10; "" when the
	// volume has neither (most magazines, some scans).
	ISBN string
	// Link is the volume's page on books.google.com — the row's link target.
	Link    string
	Pages   int
	Snippet string
}

// Byline renders a volume's authors as "A", "A and B", or "A, B and C" —
// the reading order of a book cover, not a bibliography.
func (b Book) Byline() string {
	switch len(b.Authors) {
	case 0:
		return ""
	case 1:
		return b.Authors[0]
	}
	return strings.Join(b.Authors[:len(b.Authors)-1], ", ") + " and " + b.Authors[len(b.Authors)-1]
}

// BooksClient searches Google Books. The zero value is usable: ConfigDir points
// at the user's config root (credentials.json lives under it, holding the
// optional key), and Endpoint/HTTP exist so tests point the client at a local
// server.
type BooksClient struct {
	Endpoint  string
	APIKey    string
	ConfigDir string
	HTTP      *http.Client
}

func (c *BooksClient) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return booksEndpoint
}

func (c *BooksClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// key resolves the optional quota key: an explicit override first, then the
// googlebooks block of credentials.json. An empty key is normal — the search
// simply goes out unauthenticated.
func (c *BooksClient) key() string {
	if c.APIKey != "" {
		return c.APIKey
	}
	if c.ConfigDir == "" {
		return ""
	}
	return LoadBooksAPIKey(c.ConfigDir)
}

// Search returns at most limit volumes for query (limit <= 0 means BooksLimit).
func (c *BooksClient) Search(ctx context.Context, query string, limit int) ([]Book, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty search")
	}
	if limit <= 0 {
		limit = BooksLimit
	}
	if limit > booksMaxResults {
		limit = booksMaxResults
	}
	body, err := c.fetch(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	books, err := ParseGoogleBooks(body, limit)
	if err != nil {
		return nil, errors.Wrap(err, "reading the google books answer")
	}
	if len(books) == 0 {
		return nil, errors.Errorf("no books found for %q", query)
	}
	return books, nil
}

// fetch asks the volumes endpoint for the query. The search goes out over GET
// with the query in the URL, which is the only shape the API accepts.
func (c *BooksClient) fetch(ctx context.Context, query string, limit int) (string, error) {
	endpoint := c.endpoint()
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", errors.Wrapf(err, "parsing %s", endpoint)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("maxResults", strconv.Itoa(limit))
	q.Set("printType", "books")
	if k := c.key(); k != "" {
		q.Set("key", k)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", errors.Wrap(err, "building the books request")
	}
	req.Header.Set("User-Agent", booksUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", errors.Wrap(err, "calling google books")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", errors.Wrap(err, "reading the google books answer")
	}
	if resp.StatusCode != http.StatusOK {
		// the anonymous quota is per IP and small; saying so beats a bare 429,
		// because the fix (an api_key in credentials.json) is not guessable
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			return "", errors.Errorf("google books: %s — add {\"googlebooks\":{\"api_key\":\"…\"}} to credentials.json to raise the quota",
				resp.Status)
		}
		return "", errors.Errorf("google books: %s", resp.Status)
	}
	return string(body), nil
}

// bookIdentifier is one entry of a volume's industryIdentifiers list.
type bookIdentifier struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

// booksResponse is the slice of the volumes answer the node reads.
type booksResponse struct {
	Items []struct {
		VolumeInfo struct {
			Title               string           `json:"title"`
			Subtitle            string           `json:"subtitle"`
			Authors             []string         `json:"authors"`
			Publisher           string           `json:"publisher"`
			PublishedDate       string           `json:"publishedDate"`
			Description         string           `json:"description"`
			PageCount           int              `json:"pageCount"`
			InfoLink            string           `json:"infoLink"`
			CanonicalVolumeLink string           `json:"canonicalVolumeLink"`
			IndustryIdentifiers []bookIdentifier `json:"industryIdentifiers"`
		} `json:"volumeInfo"`
	} `json:"items"`
}

// ParseGoogleBooks reads a volumes answer, keeping Google's own relevance
// order. It is exported so the JSON contract is testable without the network.
func ParseGoogleBooks(body string, limit int) ([]Book, error) {
	if limit <= 0 {
		limit = BooksLimit
	}
	var resp booksResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, errors.Wrap(err, "decoding the google books answer")
	}
	out := make([]Book, 0, len(resp.Items))
	seen := map[string]bool{}
	for _, item := range resp.Items {
		if len(out) >= limit {
			break
		}
		v := item.VolumeInfo
		title := cleanText(v.Title)
		if title == "" {
			continue // a volume with no title is nothing a row can say
		}
		link := resolveURL(v.CanonicalVolumeLink)
		if link == "" {
			link = resolveURL(v.InfoLink)
		}
		// the same edition comes back twice when a query matches both its print
		// and ebook records; the volume page is what tells them apart
		if link != "" {
			if seen[link] {
				continue
			}
			seen[link] = true
		}
		b := Book{
			Title:     title,
			Subtitle:  cleanText(v.Subtitle),
			Publisher: cleanText(v.Publisher),
			Year:      bookYear(v.PublishedDate),
			ISBN:      bookISBN(v.IndustryIdentifiers),
			Link:      link,
			Pages:     v.PageCount,
			Snippet:   cleanText(v.Description),
		}
		for _, a := range v.Authors {
			if a = cleanText(a); a != "" {
				b.Authors = append(b.Authors, a)
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// bookYear takes the year off a publishedDate, which Google returns as
// "2004", "2004-05" or "2004-05-01" depending on what the publisher filed.
func bookYear(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 4 {
		return ""
	}
	year := raw[:4]
	for _, r := range year {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return year
}

// bookISBN prefers the ISBN-13 (the current standard) and falls back to the
// ISBN-10 an older edition carries.
func bookISBN(ids []bookIdentifier) string {
	fallback := ""
	for _, id := range ids {
		v := strings.TrimSpace(id.Identifier)
		if v == "" {
			continue
		}
		switch id.Type {
		case "ISBN_13":
			return v
		case "ISBN_10":
			if fallback == "" {
				fallback = v
			}
		}
	}
	return fallback
}
