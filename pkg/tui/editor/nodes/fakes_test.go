package nodes

import (
	"context"
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/tui/compute"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The shared plugin-test harness. The whole point of the plugin API is that a
// node file tests against fakes, no editor Model needed — so fakeHost/fakeNode
// live here once and every per-node <type>_test.go file reuses them.

// fakeHost implements editor.NodeHost for plugin tests.
type fakeHost struct {
	db        *database.DB
	stores    map[string]map[string]any
	flash     string
	deps      map[string]bool
	configDir string
	compute   func() <-chan compute.Event
}

func newFakeHost(t *testing.T) *fakeHost {
	return &fakeHost{db: database.InitTestMemoryDB(t), stores: map[string]map[string]any{}}
}

func (f *fakeHost) NodeStore(uuid string) map[string]any {
	if f.stores[uuid] == nil {
		f.stores[uuid] = map[string]any{}
	}
	return f.stores[uuid]
}
func (f *fakeHost) NodeDB() *database.DB  { return f.db }
func (f *fakeHost) NodeConfigDir() string { return f.configDir }
func (f *fakeHost) NodeFlash(msg string)  { f.flash = msg }
func (f *fakeHost) NodeDepOK(b string) bool {
	if f.deps == nil {
		return true
	}
	ok, probed := f.deps[b]
	return !probed || ok
}

// NodeSetGenerated mirrors the editor's: swap the node's children of type typ
// for one row each, keeping everything else.
func (f *fakeHost) NodeSetGenerated(n editor.NodeRef, typ string, rows []editor.NodeRow) int {
	fn, ok := n.(*fakeNode)
	if !ok {
		return 0
	}
	var kept []*fakeNode
	for _, c := range fn.kids {
		if c.typ != typ {
			kept = append(kept, c)
		}
	}
	made := make([]*fakeNode, 0, len(rows))
	for i, r := range rows {
		made = append(made, &fakeNode{uuid: fmt.Sprintf("%s-gen%d", fn.uuid, i), typ: typ,
			text: r.Text, url: r.URL, parent: fn})
	}
	fn.kids = append(made, kept...)
	return len(made)
}
func (f *fakeHost) NodeComputeTurn(context.Context, string, string, string) (<-chan compute.Event, error) {
	return f.compute(), nil
}

// fakeNode implements editor.NodeRef.
type fakeNode struct {
	uuid, typ, text, path string
	url                   string // the link target a generated row carries
	parent                *fakeNode
	kids                  []*fakeNode
}

func (n *fakeNode) UUID() string     { return n.uuid }
func (n *fakeNode) Type() string     { return n.typ }
func (n *fakeNode) Text() string     { return n.text }
func (n *fakeNode) SetText(s string) { n.text = s }
func (n *fakeNode) PathChip() string { return n.path }
func (n *fakeNode) Parent() (editor.NodeRef, bool) {
	if n.parent == nil {
		return nil, false
	}
	return n.parent, true
}
func (n *fakeNode) Siblings() []editor.NodeRef {
	if n.parent == nil {
		return nil
	}
	out := make([]editor.NodeRef, 0, len(n.parent.kids))
	for _, k := range n.parent.kids {
		out = append(out, k)
	}
	return out
}
func (n *fakeNode) Children() []editor.NodeRef {
	out := make([]editor.NodeRef, 0, len(n.kids))
	for _, k := range n.kids {
		out = append(out, k)
	}
	return out
}
func (n *fakeNode) Is(o editor.NodeRef) bool {
	fo, ok := o.(*fakeNode)
	return ok && fo == n
}
