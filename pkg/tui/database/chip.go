package database

import (
	"path/filepath"
	"strings"

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
	default:
		return c.Value
	}
}

// ChipExpand is a chip's full underlying value (e.g. the absolute path).
func ChipExpand(c Chip) string {
	if c.Kind == "tag" {
		return "#" + c.Value
	}
	return c.Value
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
	for j := 0; j < len(runes); j++ {
		if runes[j] != ChipSentinel {
			continue
		}
		k := j + 1
		for k < len(runes) && runes[k] != ChipSentinel {
			k++
		}
		if k >= len(runes) {
			break
		}
		b.WriteString(string(runes[i:j]))
		id := string(runes[j+1 : k])
		if c, ok := chips[id]; ok {
			b.WriteString(f(c))
		} else {
			b.WriteString("@?")
		}
		i = k + 1
		j = k
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
	Kind  string `json:"kind"`  // path, date, tag, …
	Value string `json:"value"` // the full underlying data (e.g. the absolute path)
}

// LoadChips returns every chip keyed by id.
func LoadChips(db *DB) (map[string]Chip, error) {
	rows, err := db.Query("SELECT id, kind, value FROM chips")
	if err != nil {
		return nil, errors.Wrap(err, "loading chips")
	}
	defer rows.Close()
	out := map[string]Chip{}
	for rows.Next() {
		var c Chip
		if err := rows.Scan(&c.ID, &c.Kind, &c.Value); err != nil {
			return nil, errors.Wrap(err, "scanning chip")
		}
		out[c.ID] = c
	}
	return out, nil
}

// GetChip returns one chip by id.
func GetChip(db *DB, id string) (Chip, error) {
	var c Chip
	err := db.QueryRow("SELECT id, kind, value FROM chips WHERE id = ?", id).Scan(&c.ID, &c.Kind, &c.Value)
	return c, errors.Wrapf(err, "getting chip %s", id)
}

// UpsertChip inserts or overwrites a chip.
func UpsertChip(db *DB, c Chip) error {
	_, err := db.Exec(
		"INSERT INTO chips (id, kind, value) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, value = excluded.value",
		c.ID, c.Kind, c.Value)
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
