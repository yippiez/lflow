// Package zotero is the Zotero integration: it reads the local Zotero library
// (the desktop app's zotero.sqlite) and turns its entries into citable
// references. The editor's zotero chip carries one such reference inline, and
// resolves it to two destinations — the LOCAL Zotero app through its
// "zotero://select/…" handler, and the CLOUD web library on zotero.org.
//
// Nothing here writes: the Zotero database is opened through a throwaway
// snapshot copy, so a running Zotero is never disturbed and the outline never
// depends on Zotero's schema staying still.
package zotero

import (
	"fmt"
	"regexp"
	"strings"
)

// Item is one bibliographic entry, flattened out of Zotero's itemData tables
// into the handful of fields a citation needs.
type Item struct {
	Key      string // Zotero item key — stable, and what the zotero:// URI names
	GroupID  string // group library id; "" for the personal library
	Type     string // Zotero item type (journalArticle, book, …)
	Title    string
	Creators []string // author surnames in Zotero's own order
	Year     string   // 4-digit year pulled out of Zotero's multipart date field
	Journal  string   // publicationTitle / bookTitle / proceedingsTitle
	DOI      string
	URL      string
	Modified int64 // dateModified as unix seconds, for recency ranking
}

// Label is the compact inline citation the chip displays: author-year in the
// usual academic shorthand ("Smith et al. 2020"). It never returns "" — a
// creatorless, undated entry falls back to its short title.
func (it Item) Label() string {
	who := ""
	switch len(it.Creators) {
	case 0:
	case 1:
		who = it.Creators[0]
	case 2:
		who = it.Creators[0] + " & " + it.Creators[1]
	default:
		who = it.Creators[0] + " et al."
	}
	if who == "" {
		who = shortTitle(it.Title)
	}
	if who == "" {
		who = it.Key
	}
	if it.Year == "" {
		return who
	}
	return who + " " + it.Year
}

// Citation is the full reference line — what a machine surface (export, agent
// context, a bash run) should see instead of the compact chip. Roughly APA:
// "Smith, J. & Doe, A. (2020). Title. Journal. https://doi.org/10.…".
func (it Item) Citation() string {
	var parts []string
	if who := strings.Join(it.Creators, ", "); who != "" {
		if it.Year != "" {
			parts = append(parts, who+" ("+it.Year+")")
		} else {
			parts = append(parts, who)
		}
	} else if it.Year != "" {
		parts = append(parts, "("+it.Year+")")
	}
	if it.Title != "" {
		parts = append(parts, it.Title)
	}
	if it.Journal != "" {
		parts = append(parts, it.Journal)
	}
	if it.DOI != "" {
		parts = append(parts, DOIURL(it.DOI))
	} else if it.URL != "" {
		parts = append(parts, it.URL)
	}
	if len(parts) == 0 {
		return it.Key
	}
	return strings.Join(parts, ". ")
}

// searchText is the haystack one query matches against: everything a user might
// half-remember about a paper.
func (it Item) searchText() string {
	return strings.ToLower(strings.Join([]string{
		strings.Join(it.Creators, " "), it.Year, it.Title, it.Journal, it.DOI, it.Type,
	}, " "))
}

// shortTitle is the first few words of a title, for a creatorless entry's label.
func shortTitle(title string) string {
	words := strings.Fields(title)
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, " ")
}

// ── references: the chip value ─────────────────────────────────────────────

// Ref identifies one Zotero entry: its item key plus the library it lives in.
// It is what a zotero chip's value encodes, and it round-trips through the
// "zotero://select/…" URI so the stored value IS the local-open target — no
// private encoding to keep in sync.
type Ref struct {
	Key     string
	GroupID string // "" = the personal library
}

// reSelect matches Zotero's own select URI, in both its personal-library and
// group-library spellings.
var reSelect = regexp.MustCompile(`^zotero://select/(?:library|groups/([0-9]+))/items/([A-Za-z0-9]+)/?$`)

// RefOf is the reference for an item.
func RefOf(it Item) Ref { return Ref{Key: it.Key, GroupID: it.GroupID} }

// LocalURI is the reference's "open it in the Zotero app" target. Zotero
// registers this scheme with the desktop (including Windows), so handing the
// URI to the OS selects the entry in a running Zotero — or starts one.
func (r Ref) LocalURI() string {
	if r.GroupID != "" {
		return "zotero://select/groups/" + r.GroupID + "/items/" + r.Key
	}
	return "zotero://select/library/items/" + r.Key
}

// PDFURI opens the reference in Zotero's own PDF reader rather than selecting
// it in the library — the reference here being an ATTACHMENT's key. An
// annotation key (optional) selects one mark inside the document, which is how
// a mirrored highlight points back at the page it came from.
func (r Ref) PDFURI(annotation string) string {
	base := "zotero://open-pdf/library/items/" + r.Key
	if r.GroupID != "" {
		base = "zotero://open-pdf/groups/" + r.GroupID + "/items/" + r.Key
	}
	if annotation != "" {
		return base + "?annotation=" + annotation
	}
	return base
}

// WebURL is the reference's zotero.org web-library address. A group library
// needs nothing but its id; the personal library is addressed by the account's
// username, which the caller resolves (from the local library's own account
// settings, or credentials.json) — an empty username returns ok=false so the
// caller can fall back to the entry's DOI.
func (r Ref) WebURL(username string) (string, bool) {
	if r.GroupID != "" {
		return "https://www.zotero.org/groups/" + r.GroupID + "/items/" + r.Key + "/library", true
	}
	if username == "" {
		return "", false
	}
	return "https://www.zotero.org/" + username + "/items/" + r.Key + "/library", true
}

// ParseRef reads a reference back out of a chip value (a select URI). Anything
// else returns ok=false.
func ParseRef(value string) (Ref, bool) {
	mm := reSelect.FindStringSubmatch(strings.TrimSpace(value))
	if mm == nil {
		return Ref{}, false
	}
	return Ref{Key: mm[2], GroupID: mm[1]}, true
}

// DOIURL turns a bare DOI into a resolvable URL, leaving an already-resolved
// one alone.
func DOIURL(doi string) string {
	doi = strings.TrimSpace(doi)
	if doi == "" {
		return ""
	}
	if strings.HasPrefix(doi, "http://") || strings.HasPrefix(doi, "https://") {
		return doi
	}
	return "https://doi.org/" + strings.TrimPrefix(doi, "doi:")
}

// reYear finds a plausible publication year inside Zotero's multipart date
// field (values look like "2020-05-13 2020-05-13" or "0000-00-00 2020").
var reYear = regexp.MustCompile(`\b(1[5-9][0-9]{2}|20[0-9]{2}|21[0-9]{2})\b`)

// yearOf extracts the year from a Zotero date value, or "".
func yearOf(date string) string {
	return reYear.FindString(date)
}

// Format renders an item for a picker row: the label, then the title and the
// venue in whatever room is left. Pure string work — the caller adds color.
func Format(it Item) (label, detail string) {
	detail = it.Title
	if it.Journal != "" {
		detail = fmt.Sprintf("%s · %s", detail, it.Journal)
	}
	return it.Label(), strings.TrimSpace(detail)
}
