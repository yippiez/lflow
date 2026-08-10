package database

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lflow/lflow/tui/utils"
	"github.com/pkg/errors"
)

// The chip text-pattern vocabulary: the recognized inline forms — #tags,
// canonical dates, and [label](target) links — in a plain node name, and the
// Chipify machinery that rewrites them into chip anchors. It is the single
// source of truth shared by the database write path (ChipifyName chipifies
// text passed to add/edit) and the editor (which detects the same spans to
// render and auto-chipify on the fly).
//
// Only the patterns that have an unambiguous inline form live here. Chip kinds
// created through an explicit picker rather than typed text (e.g. icon chips)
// are never auto-detected.

// Chip kind keys. These are plain strings matching the editor's chip-kind
// registry and the values stored in the chips table.
const (
	kindTag    = "tag"
	kindDate   = "date"
	kindLink   = "link"
	kindZotero = "zotero"
)

// zoteroScheme prefixes a Zotero select URI. A "[label](target)" whose target
// is one is a CITATION, not a plain web link, so it chipifies as a zotero chip
// — that is how `lflow add` writes a citation the editor lights up.
const zoteroScheme = "zotero://select/"

// span is one detected inline form to chipify.
type span struct {
	start, end         int
	kind, value, label string
}

// Chipify rewrites every detected inline form in name into a chip anchor by
// calling mk(kind, value, label), which records the chip and returns its anchor
// (or "" to decline, leaving the original text in place). Overlapping matches
// resolve to the earliest — a #tag inside a [link] label stays part of the link.
// The note text is never chipified; only names carry chip anchors.
func Chipify(name string, mk func(kind, value, label string) string) string {
	runes := []rune(name)
	var spans []span
	for _, sp := range TagSpans(name) {
		spans = append(spans, span{sp[0], sp[1], kindTag, strings.TrimPrefix(string(runes[sp[0]:sp[1]]), "#"), ""})
	}
	for _, sp := range DateSpans(name) {
		spans = append(spans, span{sp[0], sp[1], kindDate, string(runes[sp[0]:sp[1]]), ""})
	}
	for _, sp := range linkSpans(name) {
		kind := kindLink
		if strings.HasPrefix(sp.Target, zoteroScheme) {
			kind = kindZotero
		}
		spans = append(spans, span{sp.Start, sp.End, kind, sp.Target, sp.Label})
	}
	if len(spans) == 0 {
		return name
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	var b strings.Builder
	prev, last := 0, -1
	for _, s := range spans {
		if s.start < last {
			continue // overlapping an earlier span — keep the earlier
		}
		b.WriteString(string(runes[prev:s.start]))
		if anchor := mk(s.kind, s.value, s.label); anchor != "" {
			b.WriteString(anchor)
		} else {
			b.WriteString(string(runes[s.start:s.end]))
		}
		prev, last = s.end, s.end
	}
	b.WriteString(string(runes[prev:]))
	return b.String()
}

// ── the tag chip: a #word at a left boundary ────────────────────────────────

// ReTag matches a #word tag at a left boundary (start of text or a non-word
// char) so bare '#'s and mid-word hashes are ignored. The word must start with a
// letter or underscore (#1, #42 mean literal "number one"), may contain inner
// hyphens (#multi-word) but not a trailing one. Submatch 2 is the #word.
var ReTag = regexp.MustCompile(`(^|[^\p{L}\p{N}_#])(#[\p{L}_][\p{L}\p{N}_]*(?:-[\p{L}\p{N}_]+)*)`)

// TagSpans returns the [start,end) rune ranges of each tag's "#word" run.
func TagSpans(name string) [][2]int {
	var spans [][2]int
	for _, loc := range ReTag.FindAllStringSubmatchIndex(name, -1) {
		s, e := loc[4], loc[5] // submatch 2 — the #word, minus any left boundary
		if s < 0 {
			continue
		}
		spans = append(spans, [2]int{utf8.RuneCountInString(name[:s]), utf8.RuneCountInString(name[:e])})
	}
	return spans
}

// TagsIn returns the lowercased tag words (without the leading '#') in text.
func TagsIn(text string) []string {
	var out []string
	for _, m := range ReTag.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.ToLower(m[2][1:]))
	}
	return out
}

// ── the date chip: a canonical YYYY-MM-DD (optionally with HH:MM) ───────────

// ReISO matches a canonical date: YYYY-MM-DD optionally followed by HH:MM.
var ReISO = regexp.MustCompile(`(\d{4})-(\d{1,2})-(\d{1,2})(?:[ T](\d{1,2}):(\d{2}))?`)

// DateSpans returns the rune ranges [start,end) of every canonical date in the
// name — a valid YYYY-MM-DD optionally followed by HH:MM, on its own word
// boundary. Natural-language phrases (e.g. "tomorrow") are not matched: only
// already-canonical dates become chips.
func DateSpans(name string) [][2]int {
	var spans [][2]int
	for _, loc := range ReISO.FindAllStringSubmatchIndex(name, -1) {
		if !utils.WordBound(name, loc[0], loc[1]) {
			continue
		}
		group := func(i int) string {
			if loc[2*i] >= 0 {
				return name[loc[2*i]:loc[2*i+1]]
			}
			return ""
		}
		if _, ok := BuildDate(utils.Atoi(group(1)), utils.Atoi(group(2)), utils.Atoi(group(3)), utils.Atoi(group(4)), utils.Atoi(group(5)), time.UTC); !ok {
			continue
		}
		spans = append(spans, [2]int{utf8.RuneCountInString(name[:loc[0]]), utf8.RuneCountInString(name[:loc[1]])})
	}
	return spans
}

// BuildDate validates the parts and returns the time, or false on nonsense like
// month 13 or february 30.
func BuildDate(year, month, day, hour, min int, loc *time.Location) (time.Time, bool) {
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || min > 59 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, min, 0, 0, loc)
	if t.Day() != day || t.Month() != time.Month(month) {
		return time.Time{}, false
	}
	return t, true
}

// ── the link chip: a markdown-style "[label](target)" ───────────────────────

// reLink matches a markdown-style link "[label](target)".
var reLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// linkSpan is one "[label](target)" match in rune offsets, with its parts.
type linkSpan struct {
	Start, End int
	Label      string
	Target     string
}

// linkSpans returns every "[label](target)" link in name, in order.
func linkSpans(name string) []linkSpan {
	var out []linkSpan
	for _, loc := range reLink.FindAllStringSubmatchIndex(name, -1) {
		out = append(out, linkSpan{
			Start:  utf8.RuneCountInString(name[:loc[0]]),
			End:    utf8.RuneCountInString(name[:loc[1]]),
			Label:  name[loc[2]:loc[3]],
			Target: name[loc[4]:loc[5]],
		})
	}
	return out
}

// ── the services a link can point at ────────────────────────────────────────

// A link chip whose target is a known service — the Google suite and the
// assistants — carries that service's unicode mark beside its name: "→▦ Q3
// budget" instead of "→Q3 budget". The mark is the only difference; a service
// link keeps the same arrow, color, underline and gestures as every other link.
//
// The service is DERIVED from the target URL and never stored: no new chip kind,
// no column, no migration. A Google link made long before this file existed
// picks up its mark the moment it is rendered, and retargeting a chip remarks
// it.

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
	// catalog uses), Gemini's spark, and for ChatGPT the benzene ring its knotted
	// logo reads as. The ring is deliberately NOT the ✳ asterisk: that codepoint
	// carries emoji presentation, so terminals paint it wide and in color, which
	// is both the wrong weight for a quiet link chip and Claude Code's own idle
	// glyph (see multiplexer/detect.go). ⌬ is text-presentation and monochrome.
	{Key: "claude", Label: "Claude", Glyph: "✽", hosts: []string{"claude.ai", "claude.com"}},
	{Key: "gemini", Label: "Gemini", Glyph: "✦", hosts: []string{"gemini.google.com"}},
	{Key: "chatgpt", Label: "ChatGPT", Glyph: "⌬", hosts: []string{"chatgpt.com", "chat.openai.com"}},
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

// every read surface (CLI, export, search) resolve anchors through the chip store.
const ChipSentinel = '￼'

// ChipAnchor builds the in-text anchor for a chip id.
func ChipAnchor(id string) string {
	return string(ChipSentinel) + id + string(ChipSentinel)
}

// HasAnchor reports whether name contains any chip anchor.
func HasAnchor(name string) bool { return strings.ContainsRune(name, ChipSentinel) }

// ChipDisplay is a chip's compact form (e.g. "#tag"). Keep in sync with
// the editor's chip-kind registry; this is the lower-level copy CLI surfaces use.
func ChipDisplay(c Chip) string {
	switch c.Kind {
	case "tag":
		return "#" + c.Value
	case "link":
		// a link to a known service (Google Sheets/Docs/Drive …) shows its glyph
		// and title; the editor adds the service's color on top
		if svc, ok := ServiceFor(c.Value); ok {
			return ServiceDisplay(svc, c.Label)
		}
		return linkLabel(c)
	case "zotero":
		// a citation: the compact author-year label behind the Zotero brand mark
		return "Z " + linkLabel(c)
	case "cmd":
		return "$" + c.Value
	case "icon":
		// value is the glyph; label holds the shortcode (editor-only color key)
		return c.Value
	default:
		return c.Value
	}
}

// ChipExpand is a chip's full underlying value (e.g. the absolute path). A link
// expands to a markdown-style "[name](target)" so both halves survive export.
func ChipExpand(c Chip) string {
	switch c.Kind {
	case "tag":
		return "#" + c.Value
	case "link", "zotero":
		return "[" + linkLabel(c) + "](" + c.Value + ")"
	default:
		return c.Value
	}
}

// linkLabel is a link chip's display name, falling back to the target when the
// name is empty so a link is never blank.
func linkLabel(c Chip) string {
	if c.Label != "" {
		return c.Label
	}
	return c.Value
}

// AnchorSpan is one chip anchor's rune range [Start,End) (both sentinels
// included) and the chip id it carries.
type AnchorSpan struct {
	Start, End int
	ID         string
}

// AnchorSpans returns every well-formed anchor in runes, in order. This is the
// one place the "￼<id>￼" sentinel format is parsed — every anchor-aware surface
// (this file's resolvers and the editor's caret/layout code) walks these spans,
// so the scan can never drift into two subtly different loops.
func AnchorSpans(runes []rune) []AnchorSpan {
	var spans []AnchorSpan
	for i := 0; i < len(runes); i++ {
		if runes[i] != ChipSentinel {
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] != ChipSentinel {
			j++
		}
		if j >= len(runes) {
			break // unterminated anchor: ignore the trailing sentinel
		}
		spans = append(spans, AnchorSpan{Start: i, End: j + 1, ID: string(runes[i+1 : j])})
		i = j
	}
	return spans
}

// resolveAnchors rewrites each anchor in name using f(chip). A missing record
// degrades to "@?" so a raw anchor never leaks to a read surface.
func resolveAnchors(name string, chips map[string]Chip, f func(Chip) string) string {
	if !HasAnchor(name) {
		return name
	}
	runes := []rune(name)
	var b strings.Builder
	i := 0
	for _, sp := range AnchorSpans(runes) {
		b.WriteString(string(runes[i:sp.Start]))
		if c, ok := chips[sp.ID]; ok {
			b.WriteString(f(c))
		} else {
			b.WriteString("@?")
		}
		i = sp.End
	}
	b.WriteString(string(runes[i:]))
	return b.String()
}

// DisplayAnchors resolves every anchor in name to its compact display form —
// for human-readable surfaces (node list, grep).
func DisplayAnchors(name string, chips map[string]Chip) string {
	return resolveAnchors(name, chips, ChipDisplay)
}

// ExpandAnchors resolves every anchor in name to its full value — for machine
// surfaces (json export, scripts, search).
func ExpandAnchors(name string, chips map[string]Chip) string {
	return resolveAnchors(name, chips, ChipExpand)
}

// Chip is an inline structured token referenced by an anchor in a node's name
// (see the chip-kind registry in tui/editor). The name text holds an opaque
// anchor carrying the chip id; the chip's real data lives here.
type Chip struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`  // date, tag, link, …
	Value string `json:"value"` // the full underlying data (e.g. a link target)
	Label string `json:"label"` // display name; used by link chips, empty for path/date/tag
}

// ChipifyName rewrites the inline forms in a node name — #tags, canonical dates,
// and [label](target) links — into chip anchors, recording each chip as it goes.
// It is how CLI add/edit create the same chips the editor makes inline; the
// returned name carries opaque anchors, so every read surface resolves them back.
func ChipifyName(db *DB, name string) (string, error) {
	var firstErr error
	out := Chipify(name, func(kind, value, label string) string {
		id, err := utils.GenerateUUID()
		if err != nil {
			if firstErr == nil {
				firstErr = errors.Wrap(err, "generating chip id")
			}
			return ""
		}
		if err := UpsertChip(db, Chip{ID: id, Kind: kind, Value: value, Label: label}); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return ""
		}
		return ChipAnchor(id)
	})
	return out, firstErr
}

// LoadChips returns every chip keyed by id.
func LoadChips(db *DB) (map[string]Chip, error) {
	rows, err := db.Query("SELECT id, kind, value, label FROM chips")
	if err != nil {
		return nil, errors.Wrap(err, "loading chips")
	}
	defer rows.Close()
	out := map[string]Chip{}
	for rows.Next() {
		var c Chip
		if err := rows.Scan(&c.ID, &c.Kind, &c.Value, &c.Label); err != nil {
			return nil, errors.Wrap(err, "scanning chip")
		}
		out[c.ID] = c
	}
	return out, nil
}

// GetChip returns one chip by id.
func GetChip(db *DB, id string) (Chip, error) {
	var c Chip
	err := db.QueryRow("SELECT id, kind, value, label FROM chips WHERE id = ?", id).Scan(&c.ID, &c.Kind, &c.Value, &c.Label)
	return c, errors.Wrapf(err, "getting chip %s", id)
}

// UpsertChip inserts or overwrites a chip.
func UpsertChip(db *DB, c Chip) error {
	_, err := db.Exec(
		"INSERT INTO chips (id, kind, value, label) VALUES (?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, value = excluded.value, label = excluded.label",
		c.ID, c.Kind, c.Value, c.Label)
	return errors.Wrapf(err, "upserting chip %s", c.ID)
}

// DeleteChip removes a chip by id.
func DeleteChip(db *DB, id string) error {
	_, err := db.Exec("DELETE FROM chips WHERE id = ?", id)
	return errors.Wrapf(err, "deleting chip %s", id)
}

// GCChips drops chip rows no live node name or note references — orphans left by
// deleted or rewritten text. Anchors embed the id verbatim, so instr suffices.
func GCChips(db *DB) error {
	_, err := db.Exec(`DELETE FROM chips WHERE id NOT IN (
		SELECT chips.id FROM chips JOIN nodes ON nodes.deleted = 0
			AND (instr(nodes.name, chips.id) > 0 OR instr(nodes.note, chips.id) > 0)
	)`)
	return errors.Wrap(err, "gc chips")
}

// nodeLinkScheme is the chip value prefix for a link that points at a node
// (kept in lockstep with the editor's nodeLinkScheme in link.go).
const nodeLinkScheme = "lflow://node/"

// BacklinkNodes returns every non-deleted node that references targetUUID:
// mirrors (mirror_of = target) and nodes whose name embeds a link chip whose
// value is lflow://node/<targetUUID>. Deduped; order is unspecified — the
// /backlinks finder sorts by star / subtree weight / recency.
func BacklinkNodes(db *DB, targetUUID string) ([]Node, error) {
	if targetUUID == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var ret []Node
	add := func(n Node) {
		if seen[n.UUID] || n.UUID == targetUUID {
			return
		}
		seen[n.UUID] = true
		ret = append(ret, n)
	}

	// mirrors of the target (empty-name rows are kept — they are the backlinks)
	mirrors, err := GetNodesWhere(db, "mirror_of = ? AND deleted = 0", targetUUID)
	if err != nil {
		return nil, errors.Wrap(err, "querying mirror backlinks")
	}
	for _, n := range mirrors {
		add(n)
	}

	// nodes that embed a link chip targeting this node
	rows, err := db.Query(`
		SELECT DISTINCT `+nodeColumns+`
		FROM nodes
		JOIN chips ON chips.kind = 'link' AND chips.value = ? AND instr(nodes.name, chips.id) > 0
		WHERE nodes.deleted = 0`,
		nodeLinkScheme+targetUUID)
	if err != nil {
		return nil, errors.Wrap(err, "querying link backlinks")
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, errors.Wrap(err, "scanning link backlink")
		}
		add(n)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating link backlinks")
	}
	return ret, nil
}

// BacklinkCounts returns, for every referenced node, how many non-deleted nodes
// reference it — mirrors of it plus nodes whose name embeds a link chip
// targeting it. One pass over the whole outline, keyed by the referenced
// node's bare uuid (mirrors) or its lflow://node/<uuid> chip value (links), so
// both count into the same bucket. The editor's row suffix ("3 backlinks")
// reads this; /backlinks lists the referrers themselves.
func BacklinkCounts(db *DB) (map[string]int, error) {
	rows, err := db.Query(`
		SELECT mirror_of FROM nodes WHERE deleted = 0 AND mirror_of != ''
		UNION ALL
		SELECT c.value FROM nodes
		JOIN chips c ON c.kind = 'link' AND c.value LIKE 'lflow://node/%' AND instr(nodes.name, c.id) > 0
		WHERE nodes.deleted = 0`)
	if err != nil {
		return nil, errors.Wrap(err, "querying backlink counts")
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, errors.Wrap(err, "scanning backlink count")
		}
		if target == "" {
			continue
		}
		counts[strings.TrimPrefix(target, nodeLinkScheme)]++
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating backlink counts")
	}
	return counts, nil
}
