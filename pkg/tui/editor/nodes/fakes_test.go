package nodes

import (
	"context"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// The shared plugin-test harness. The whole point of the plugin API is that a
// node file tests against fakes, no editor Model needed — so fakeHost/fakeNode
// live here once and every per-node <type>_test.go file reuses them.

// fakeHost implements editor.NodeHost for plugin tests.
type fakeHost struct {
	db      *database.DB
	stores  map[string]map[string]any
	flash   string
	scroll  int
	deps    map[string]bool
	compute func() <-chan tag.Event
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
func (f *fakeHost) NodeDB() *database.DB { return f.db }
func (f *fakeHost) NodeFlash(msg string) { f.flash = msg }
func (f *fakeHost) NodeScroll(delta int) {
	f.scroll += delta
	if f.scroll < 0 {
		f.scroll = 0
	}
}
func (f *fakeHost) NodeDepOK(b string) bool {
	if f.deps == nil {
		return true
	}
	ok, probed := f.deps[b]
	return !probed || ok
}
func (f *fakeHost) NodeDefaultAgent() string { return "Pi" }
func (f *fakeHost) NodeAgentGate(string) (string, bool) {
	return "", false
}
func (f *fakeHost) NodeComputeTurn(context.Context, string, string, string, string) (<-chan tag.Event, error) {
	return f.compute(), nil
}

// fakeNode implements editor.NodeRef.
type fakeNode struct {
	uuid, typ, text, path string
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
