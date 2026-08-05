// Outline rendering: a node subtree as markdown, plain text or JSON for the
// scriptable command surface.
package database

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/pkg/errors"
)

// chip anchors in a node name are resolved at render time. The store is loaded
// once per process (the CLI is one-shot) — display form for human output,
// expanded value for machine output (JSON/export).
var (
	chipCacheOnce sync.Once
	chipCache     map[string]Chip
)

func chipsFor(db *DB) map[string]Chip {
	chipCacheOnce.Do(func() { chipCache, _ = LoadChips(db) })
	return chipCache
}

// JSONNode is the nested JSON representation of a node.
type JSONNode struct {
	UUID        string     `json:"uuid"`
	Name        string     `json:"name"`
	Note        string     `json:"note,omitempty"`
	Type        string     `json:"type"`
	MirrorOf    string     `json:"mirror_of,omitempty"`
	CompletedAt int64      `json:"completed_at,omitempty"`
	Children    []JSONNode `json:"children"`
}

// resolveName returns the renderable name of a node: mirrors show the original's
// name (same node everywhere), and chip anchors resolve to their display form
// (human output) or full value (expand=true, for JSON/export).
func resolveName(db *DB, n Node, expand bool) string {
	name := n.Name
	if n.MirrorOf != "" {
		orig, err := GetNode(db, n.MirrorOf)
		if err != nil {
			return "(missing mirror)"
		}
		name = orig.Name
	}
	if HasAnchor(name) {
		if expand {
			return ExpandAnchors(name, chipsFor(db))
		}
		return DisplayAnchors(name, chipsFor(db))
	}
	return name
}

// BuildJSON builds the nested JSON tree for a node.
func BuildJSON(db *DB, root Node, depth int, includeCompleted bool) (JSONNode, error) {
	ret := JSONNode{
		UUID:        root.UUID,
		Name:        resolveName(db, root, true), // machine output: full value
		Note:        root.Note,
		Type:        root.Type,
		MirrorOf:    root.MirrorOf,
		CompletedAt: root.CompletedAt,
		Children:    []JSONNode{},
	}

	if depth == 0 {
		return ret, nil
	}

	children, err := GetChildren(db, root.UUID)
	if err != nil {
		return ret, err
	}
	for _, c := range children {
		if !includeCompleted && c.CompletedAt > 0 {
			continue
		}
		childJSON, err := BuildJSON(db, c, depth-1, includeCompleted)
		if err != nil {
			return ret, err
		}
		ret.Children = append(ret.Children, childJSON)
	}

	return ret, nil
}

// RenderJSON renders the subtree as indented JSON.
func RenderJSON(db *DB, root Node, depth int, includeCompleted bool) (string, error) {
	tree, err := BuildJSON(db, root, depth, includeCompleted)
	if err != nil {
		return "", errors.Wrap(err, "building json tree")
	}
	b, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return "", errors.Wrap(err, "marshalling json tree")
	}
	return string(b), nil
}

func renderLines(db *DB, root Node, depth int, includeCompleted, markdown, includeRoot bool) ([]string, error) {
	var lines []string

	var walk func(n Node, level, remaining int) error
	walk = func(n Node, level, remaining int) error {
		indent := strings.Repeat("  ", level)
		name := resolveName(db, n, false) // human output: compact display
		if n.MirrorOf != "" {
			name += " (mirror)"
		}
		if markdown {
			if n.Type == TypeJSON {
				lines = append(lines, fmt.Sprintf("%s- ```json", indent))
				for _, jl := range strings.Split(name, "\n") {
					lines = append(lines, fmt.Sprintf("%s  %s", indent, jl))
				}
				lines = append(lines, fmt.Sprintf("%s  ```", indent))
			} else {
				switch n.Type {
				case TypeH1:
					name = "# " + name
				case TypeH2:
					name = "## " + name
				case TypeH3:
					name = "### " + name
				case TypeTodo:
					if n.CompletedAt > 0 {
						name = "[x] " + name
					} else {
						name = "[ ] " + name
					}
				}
				lines = append(lines, fmt.Sprintf("%s- %s", indent, name))
			}
			if n.Note != "" {
				for _, noteLine := range strings.Split(n.Note, "\n") {
					lines = append(lines, fmt.Sprintf("%s  %s", indent, noteLine))
				}
			}
		} else {
			lines = append(lines, indent+name)
		}

		if remaining == 0 {
			return nil
		}

		children, err := GetChildren(db, n.UUID)
		if err != nil {
			return err
		}
		for _, c := range children {
			if !includeCompleted && c.CompletedAt > 0 {
				continue
			}
			if err := walk(c, level+1, remaining-1); err != nil {
				return err
			}
		}
		return nil
	}

	if includeRoot {
		if err := walk(root, 0, depth); err != nil {
			return nil, err
		}
	} else {
		children, err := GetChildren(db, root.UUID)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			if !includeCompleted && c.CompletedAt > 0 {
				continue
			}
			if err := walk(c, 0, depth-1); err != nil {
				return nil, err
			}
		}
	}

	return lines, nil
}

// RenderMarkdown renders the subtree (children of root) as a markdown outline.
// depth < 0 means unlimited.
func RenderMarkdown(db *DB, root Node, depth int, includeCompleted bool) (string, error) {
	lines, err := renderLines(db, root, depth, includeCompleted, true, false)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// RenderText renders the subtree as plain indented names.
func RenderText(db *DB, root Node, depth int, includeCompleted bool) (string, error) {
	lines, err := renderLines(db, root, depth, includeCompleted, false, false)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}
