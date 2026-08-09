package crypto

import (
	"encoding/json"

	"github.com/pkg/errors"
)

// Content is the cleartext a vault holds: the Encrypted node's real title and
// note, plus its entire subtree. This is the vault's wire format, which is why
// it lives beside the sealing rather than in the editor — the bytes that go
// through Seal are defined here, once, so the editor and the Encrypted Query
// node cannot drift into two readings of the same blob.
//
// The subtree is stored as a nested tree rather than as node rows because it is
// NOT node rows: the children of an Encrypted node have no uuid, no rank and no
// parent_uuid, because they are never in the nodes table. They exist as
// database rows only in the sense that a paragraph exists inside a JPEG.
type Content struct {
	Title    string `json:"title"`
	Note     string `json:"note,omitempty"`
	Children []Node `json:"children,omitempty"`
}

// Node is one node inside a vault. It carries the presentation state that
// survives a lock/unlock cycle and nothing else — no uuid (a vault child gets a
// fresh one each time it is materialized), no timestamps that would let a
// bystander correlate edits with the blob's mtime.
type Node struct {
	Name        string `json:"name"`
	Note        string `json:"note,omitempty"`
	Type        string `json:"type,omitempty"`
	Style       string `json:"style,omitempty"`
	Collapsed   bool   `json:"collapsed,omitempty"`
	Starred     bool   `json:"starred,omitempty"`
	Priority    string `json:"priority,omitempty"`
	CompletedAt int64  `json:"completed_at,omitempty"`
	Children    []Node `json:"children,omitempty"`
}

// MarshalContent renders the cleartext for sealing.
func MarshalContent(c Content) ([]byte, error) {
	b, err := json.Marshal(c)
	return b, errors.Wrap(err, "marshalling vault content")
}

// UnmarshalContent parses opened cleartext.
func UnmarshalContent(b []byte) (Content, error) {
	var c Content
	err := json.Unmarshal(b, &c)
	return c, errors.Wrap(err, "parsing vault content")
}

// Path is a node's position inside a vault, root first — what an Encrypted
// Query hit shows as its breadcrumb, since a vault child has no uuid to point
// at and no row in the outline to jump to.
type Path []string

// Walk visits every node in the vault depth-first, passing the ancestor names
// above it. It is how the Encrypted Query searches: the only way to match text
// inside a vault is to open it and read, because nothing about it was indexed.
func (c Content) Walk(fn func(n Node, path Path)) {
	var walk func(ns []Node, path Path)
	walk = func(ns []Node, path Path) {
		for _, n := range ns {
			fn(n, path)
			walk(n.Children, append(path, n.Name))
		}
	}
	walk(c.Children, Path{c.Title})
}

// CountNodes is the vault's size, for the "n items" a locked row can honestly
// admit to — it comes from the opened content, so a locked vault never reports
// it.
func (c Content) CountNodes() int {
	n := 0
	c.Walk(func(Node, Path) { n++ })
	return n
}
