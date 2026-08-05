package fileeditor

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/utils"
	"github.com/pkg/errors"
)

// The file codec layer: two-way binding between a source file and a node
// subtree. A codec translates between file text and a neutral in-memory tree
// of SrcNodes; the DB glue below moves that tree in and out of the scratch
// database. The full node-type × file-format translation matrix lives in
// examples/README.md — keep it in sync with the codecs here.
//
// Both directions normalize: a first save may reshape a file (python
// re-indents to the 4-space grid, rust braces regenerate from structure),
// after which parse→render is idempotent.

// SrcNode is one node of the neutral document tree — the codec-facing
// projection of a database.Node: just type, text, and shape.
type SrcNode struct {
	Type      string
	Text      string
	Note      string // per-type payload: a code fence's language tag
	Completed bool   // todo: checked
	Output    string // nlpcompute: the generated code (node_output, read-only)
	Kids      []*SrcNode
}

// Kid appends a child and returns it (parser convenience).
func (n *SrcNode) Kid(c *SrcNode) *SrcNode {
	n.Kids = append(n.Kids, c)
	return c
}

// FileCodec is one file format: parse text into SrcNodes, render them back.
type FileCodec interface {
	// Name labels the codec in messages ("markdown", "python", …).
	Name() string
	// Exts lists the file extensions (with dot) the codec claims.
	Exts() []string
	// Allowed is the set of node types this format accepts natively — the
	// file-type restriction. Types outside the set degrade on render (comment
	// fallback where the format has comments) instead of corrupting the file;
	// nil means unrestricted. The editor's /type picker is filtered by it.
	Allowed() map[string]bool
	// DefaultType is the node type a freshly typed line gets in this format's
	// editor session ("" = bullets): python statements in .py, text in .toml.
	DefaultType() string
	// Parse decomposes file text into a document forest.
	Parse(src string) ([]*SrcNode, error)
	// Render serializes a document forest back to file text.
	Render(doc []*SrcNode) (string, error)
}

// fileCodecs holds the registered codecs, in registration order.
var fileCodecs []FileCodec

// CodecForPath picks the codec claiming the path's extension.
func CodecForPath(path string) (FileCodec, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	for _, c := range fileCodecs {
		for _, e := range c.Exts() {
			if e == ext {
				return c, true
			}
		}
	}
	return nil, false
}

// CodecExts lists every supported extension, for error messages.
func CodecExts() []string {
	var out []string
	for _, c := range fileCodecs {
		out = append(out, c.Exts()...)
	}
	return out
}

// allowed builds an Allowed set from type keys.
func allowed(types ...string) map[string]bool {
	m := make(map[string]bool, len(types))
	for _, t := range types {
		m[t] = true
	}
	return m
}

// ── DB glue ─────────────────────────────────────────────────────────────────

// ParseIntoDB parses src and inserts the document forest under rootUUID.
// Line endings normalize to \n first (a CRLF file would otherwise smuggle \r
// into every node and break the codecs' line classification), and the whole
// forest inserts in ONE transaction — per-row autocommit costs an fsync per
// node, hundreds of times slower on a large file.
func ParseIntoDB(db *database.DB, rootUUID string, c FileCodec, src string) error {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	doc, err := c.Parse(src)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return errors.Wrap(err, "beginning parse transaction")
	}
	if err := insertForest(tx, rootUUID, doc); err != nil {
		tx.Rollback()
		return err
	}
	return errors.Wrap(tx.Commit(), "committing parse transaction")
}

// insertForest inserts a document forest under parent, depth-first.
func insertForest(db *database.DB, rootUUID string, doc []*SrcNode) error {
	var insert func(parent string, kids []*SrcNode) error
	insert = func(parent string, kids []*SrcNode) error {
		for rank, k := range kids {
			uuid, err := utils.GenerateUUID()
			if err != nil {
				return errors.Wrap(err, "generating uuid")
			}
			now := time.Now().UnixNano()
			n := database.Node{
				UUID: uuid, ParentUUID: parent, Rank: rank,
				Name: k.Text, Note: k.Note, Type: k.Type,
				Priority: database.PriorityUp, AddedOn: now, EditedOn: now,
			}
			if k.Completed {
				n.CompletedAt = time.Now().Unix()
			}
			if err := n.Insert(db); err != nil {
				return err
			}
			if err := insert(uuid, k.Kids); err != nil {
				return err
			}
		}
		return nil
	}
	return insert(rootUUID, doc)
}

// RenderFromDB loads rootUUID's subtree as SrcNodes (nlpcompute cells carry
// their generated code from node_output) and renders it through the codec.
// Names pass through database.ExpandAnchors — a file session can mint chip
// anchors (typing "#tag " or pasting a URL) same as the editor does, and the
// U+FFFC sentinel must never reach a saved source file (mirrors the editor's
// chip expansion).
func RenderFromDB(db *database.DB, rootUUID string, c FileCodec) (string, error) {
	chips, err := database.LoadChips(db)
	if err != nil {
		return "", err
	}
	var load func(uuid string) ([]*SrcNode, error)
	load = func(uuid string) ([]*SrcNode, error) {
		children, err := database.GetChildren(db, uuid)
		if err != nil {
			return nil, err
		}
		var out []*SrcNode
		for _, ch := range children {
			if ch.Deleted {
				continue
			}
			name := ch.Name
			if database.HasAnchor(name) {
				name = database.ExpandAnchors(name, chips)
			}
			s := &SrcNode{
				Type: ch.Type, Text: name, Note: ch.Note,
				Completed: ch.CompletedAt > 0,
			}
			if ch.Type == database.TypeNLPCompute {
				if raw, err := database.LoadNodeOutput(db, ch.UUID); err == nil && raw != "" {
					var d struct {
						Code string `json:"code"`
						Lang string `json:"lang"`
					}
					if json.Unmarshal([]byte(raw), &d) == nil {
						s.Output = d.Code
						if s.Note == "" {
							s.Note = d.Lang
						}
					}
				}
			}
			var err error
			if s.Kids, err = load(ch.UUID); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	}
	doc, err := load(rootUUID)
	if err != nil {
		return "", err
	}
	return c.Render(doc)
}

// ── shared render helpers ───────────────────────────────────────────────────

// ensureTrailingNewline normalizes a rendered document's tail.
func ensureTrailingNewline(lines []string) string {
	s := strings.Join(lines, "\n")
	s = strings.TrimRight(s, "\n \t")
	if s != "" {
		s += "\n"
	}
	return s
}
