// Package outline renders node subtrees as markdown, plain text or JSON for
// the scriptable command surface.
package outline

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/pkg/errors"
)

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

// resolveName returns the display name of a node: mirrors show the
// original's name (same node everywhere).
func resolveName(db *database.DB, n database.Node) string {
	if n.MirrorOf == "" {
		return n.Name
	}
	orig, err := database.GetNode(db, n.MirrorOf)
	if err != nil {
		return "(missing mirror)"
	}
	return orig.Name
}

// BuildJSON builds the nested JSON tree for a node.
func BuildJSON(db *database.DB, root database.Node, depth int, includeCompleted bool) (JSONNode, error) {
	ret := JSONNode{
		UUID:        root.UUID,
		Name:        resolveName(db, root),
		Note:        root.Note,
		Type:        root.Type,
		MirrorOf:    root.MirrorOf,
		CompletedAt: root.CompletedAt,
		Children:    []JSONNode{},
	}

	if depth == 0 {
		return ret, nil
	}

	children, err := database.GetChildren(db, root.UUID)
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
func RenderJSON(db *database.DB, root database.Node, depth int, includeCompleted bool) (string, error) {
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

func renderLines(db *database.DB, root database.Node, depth int, includeCompleted, markdown, includeRoot bool) ([]string, error) {
	var lines []string

	var walk func(n database.Node, level, remaining int) error
	walk = func(n database.Node, level, remaining int) error {
		indent := strings.Repeat("  ", level)
		name := resolveName(db, n)
		if n.MirrorOf != "" {
			name += " (mirror)"
		}
		if markdown {
			switch n.Type {
			case database.TypeH1:
				name = "# " + name
			case database.TypeH2:
				name = "## " + name
			case database.TypeH3:
				name = "### " + name
			case database.TypeTodo:
				if n.CompletedAt > 0 {
					name = "[x] " + name
				} else {
					name = "[ ] " + name
				}
			}
			lines = append(lines, fmt.Sprintf("%s- %s", indent, name))
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

		children, err := database.GetChildren(db, n.UUID)
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
		children, err := database.GetChildren(db, root.UUID)
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
func RenderMarkdown(db *database.DB, root database.Node, depth int, includeCompleted bool) (string, error) {
	lines, err := renderLines(db, root, depth, includeCompleted, true, false)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// RenderText renders the subtree as plain indented names.
func RenderText(db *database.DB, root database.Node, depth int, includeCompleted bool) (string, error) {
	lines, err := renderLines(db, root, depth, includeCompleted, false, false)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}
