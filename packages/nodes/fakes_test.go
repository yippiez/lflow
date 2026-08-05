package nodes

import (
	"context"
	"testing"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/nlp"
)

// The shared plugin-test harness. The whole point of the plugin API is that a
// node file tests against fakes, no editor Model needed — so fakeHost/fakeNode
// live here once and every per-node <type>_test.go file reuses them.

// fakeHost implements Host for plugin tests.
type fakeHost struct {
	db      *database.DB
	stores  map[string]map[string]any
	flash   string
	deps    map[string]bool
	compute func() (string, error)
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
func (f *fakeHost) NodeDepOK(b string) bool {
	if f.deps == nil {
		return true
	}
	ok, probed := f.deps[b]
	return !probed || ok
}
func (f *fakeHost) NodeCompute(context.Context, string, func(nlp.Event)) (string, error) {
	if f.compute == nil {
		return "", nil
	}
	return f.compute()
}

// fakeNode implements Ref.
type fakeNode struct {
	uuid, typ, text string
	parent          *fakeNode
	kids            []*fakeNode
}

func (n *fakeNode) UUID() string     { return n.uuid }
func (n *fakeNode) Type() string     { return n.typ }
func (n *fakeNode) Text() string     { return n.text }
func (n *fakeNode) SetText(s string) { n.text = s }
func (n *fakeNode) Parent() (Ref, bool) {
	if n.parent == nil {
		return nil, false
	}
	return n.parent, true
}
func (n *fakeNode) Siblings() []Ref {
	if n.parent == nil {
		return nil
	}
	out := make([]Ref, 0, len(n.parent.kids))
	for _, k := range n.parent.kids {
		out = append(out, k)
	}
	return out
}
func (n *fakeNode) Children() []Ref {
	out := make([]Ref, 0, len(n.kids))
	for _, k := range n.kids {
		out = append(out, k)
	}
	return out
}
func (n *fakeNode) Is(o Ref) bool {
	fo, ok := o.(*fakeNode)
	return ok && fo == n
}
