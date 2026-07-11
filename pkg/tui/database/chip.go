package database

import (
	"path/filepath"
	"strings"

	"github.com/lflow/lflow/pkg/tui/chiptext"
	"github.com/lflow/lflow/pkg/utils"
	"github.com/pkg/errors"
)

// ChipSentinel opens and closes an in-text anchor: "￼<id>￼". The editor and
// every read surface (CLI, export, search) resolve anchors through the chip store.
const ChipSentinel = '￼'

// ChipAnchor builds the in-text anchor for a chip id.
func ChipAnchor(id string) string {
	return string(ChipSentinel) + id + string(ChipSentinel)
}

// HasAnchor reports whether name contains any chip anchor.
func HasAnchor(name string) bool { return strings.ContainsRune(name, ChipSentinel) }

// ChipDisplay is a chip's compact form (e.g. "@readme.txt"). Keep in sync with
// the editor's chip-kind registry; this is the lower-level copy CLI surfaces use.
func ChipDisplay(c Chip) string {
	switch c.Kind {
	case "path":
		base := filepath.Base(c.Value)
		if base == "" || base == "." || base == string(filepath.Separator) {
			base = c.Value
		}
		return "›" + base
	case "tag":
		return "#" + c.Value
	case "link":
		return linkLabel(c)
	case "cmd":
		return "$" + c.Value
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
	case "link":
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
// (see the chip-kind registry in pkg/tui/editor). The name text holds an opaque
// anchor carrying the chip id; the chip's real data lives here. Local content
// for now — a path chip's value is a machine-specific absolute path.
type Chip struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`  // path, date, tag, link, …
	Value string `json:"value"` // the full underlying data (e.g. the absolute path, or a link target)
	Label string `json:"label"` // display name; used by link chips, empty for path/date/tag
}

// ChipifyName rewrites the inline forms in a node name — #tags, canonical dates,
// and [label](target) links — into chip anchors, recording each chip as it goes.
// It is how CLI add/edit create the same chips the editor makes inline; the
// returned name carries opaque anchors, so every read surface resolves them back.
func ChipifyName(db *DB, name string) (string, error) {
	var firstErr error
	out := chiptext.Chipify(name, func(kind, value, label string) string {
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

// GCChips drops chip rows no live node name references — orphans left by deleted
// or rewritten nodes. Anchors embed the id verbatim, so an instr match suffices.
func GCChips(db *DB) error {
	_, err := db.Exec(`DELETE FROM chips WHERE id NOT IN (
		SELECT chips.id FROM chips JOIN nodes ON nodes.deleted = 0 AND instr(nodes.name, chips.id) > 0
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
