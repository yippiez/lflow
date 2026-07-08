package editor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/wf"
)

// wfMockServer speaks the Workflowy nodes API over a mutable forest so tests
// can re-pull after changing the remote side.
type wfMockServer struct {
	mu    sync.Mutex
	nodes map[string]wf.Node // by id
	srv   *httptest.Server
}

func newWFMock(t *testing.T, nodes []wf.Node) *wfMockServer {
	t.Helper()
	s := &wfMockServer{nodes: map[string]wf.Node{}}
	for _, n := range nodes {
		s.nodes[n.ID] = n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		pid := r.URL.Query().Get("parent_id")
		var kids []wf.Node
		for _, n := range s.nodes {
			if n.ParentID == pid {
				kids = append(kids, n)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"nodes": kids})
	})
	mux.HandleFunc("/nodes/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		n, ok := s.nodes[r.URL.Path[len("/nodes/"):]]
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(n)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *wfMockServer) set(n wf.Node) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[n.ID] = n
}

func (s *wfMockServer) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodes, id)
}

func wfForest() []wf.Node {
	done := int64(1753120900)
	return []wf.Node{
		{ID: "wf-root", Name: "Weekly <b>plan</b>", Note: "the plan note", Priority: 1, Data: wf.NodeData{LayoutMode: "h1"}},
		{ID: "wf-a", ParentID: "wf-root", Name: "ship the API", Priority: 2, Data: wf.NodeData{LayoutMode: "todo"}},
		{ID: "wf-b", ParentID: "wf-root", Name: "write docs", Priority: 1, Data: wf.NodeData{LayoutMode: "todo"}, CompletedAt: &done},
		{ID: "wf-a1", ParentID: "wf-a", Name: "swagger spec", Priority: 1, Data: wf.NodeData{LayoutMode: "bullets"}},
	}
}

// newWFTestModel builds a DB-backed model holding one wf node pointed at the
// mock server.
func newWFTestModel(t *testing.T, srv *wfMockServer) (*Model, *item) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	root := &item{uuid: "root"}
	wfIt := &item{
		uuid: "n-wf", typ: database.TypeWF, parent: root,
		name: "https://workflowy.com/#/wfroot ignored", // ParseRef only sees the mapping after first bind
	}
	root.children = []*item{wfIt}
	tr := &tree{
		db:            db,
		root:          root,
		byUUID:        map[string]*item{"root": root, "n-wf": wfIt},
		externalNames: map[string]string{},
		snapshots:     map[string]snapshot{},
	}
	m := &Model{
		db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30,
		wfClient: &wf.Client{APIKey: "test-key", BaseURL: srv.srv.URL, HTTP: srv.srv.Client()},
		wfMap:    map[string]string{"n-wf": "wf-root"}, // bound as if the ref was parsed once
	}
	m.refreshRows()
	return m, wfIt
}

// pull drives one full alt+r round: runWF → fetch → handleWFDone.
func pull(t *testing.T, m *Model, it *item) {
	t.Helper()
	cmd := runWF(m, it)
	if cmd == nil {
		t.Fatalf("runWF returned no command (flash: %s)", m.flash)
	}
	msg, ok := cmd().(wfDoneMsg)
	if !ok {
		t.Fatal("expected a wfDoneMsg")
	}
	if msg.err != nil {
		t.Fatalf("pull failed: %v", msg.err)
	}
	m.handleWFDone(msg)
}

func TestWFPullCreatesReadonlyMirror(t *testing.T) {
	srv := newWFMock(t, wfForest())
	m, wfIt := newWFTestModel(t, srv)

	pull(t, m, wfIt)

	// root keeps its wf type but wears the fetched name, HTML stripped
	if wfIt.name != "Weekly plan" || wfIt.typ != database.TypeWF {
		t.Fatalf("root = %q type %q, want 'Weekly plan' type wf", wfIt.name, wfIt.typ)
	}
	if wfIt.note != "the plan note" {
		t.Errorf("root note = %q", wfIt.note)
	}
	// children in priority order: wf-b (1) then wf-a (2)
	if len(wfIt.children) != 2 {
		t.Fatalf("want 2 children, got %d", len(wfIt.children))
	}
	b, a := wfIt.children[0], wfIt.children[1]
	if b.name != "write docs" || a.name != "ship the API" {
		t.Fatalf("priority order wrong: %q, %q", b.name, a.name)
	}
	// translated types, readonly, completion carried over
	if a.typ != database.TypeTodo || !a.readonly {
		t.Errorf("a: type %q readonly %v, want todo readonly", a.typ, a.readonly)
	}
	if b.completedAt == 0 {
		t.Error("completed workflowy todo must complete the mirror")
	}
	// recursion + id map rows persisted
	if len(a.children) != 1 || a.children[0].name != "swagger spec" {
		t.Fatal("nested child missing")
	}
	saved, err := database.AllWFNodes(m.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 4 {
		t.Fatalf("want 4 wf map rows, got %d", len(saved))
	}
}

func TestWFRepullReconcilesInPlace(t *testing.T) {
	srv := newWFMock(t, wfForest())
	m, wfIt := newWFTestModel(t, srv)
	pull(t, m, wfIt)

	// the user parks their own note under the mirror — it must survive re-pulls
	own := &item{uuid: "mine", name: "my local note", parent: wfIt}
	wfIt.children = append(wfIt.children, own)
	m.tree.byUUID["mine"] = own

	aUUID := wfIt.children[1].uuid // "ship the API"

	// remote: rename a, delete b, add c
	srv.set(wf.Node{ID: "wf-a", ParentID: "wf-root", Name: "ship the API v2", Priority: 2, Data: wf.NodeData{LayoutMode: "todo"}})
	srv.remove("wf-b")
	srv.set(wf.Node{ID: "wf-c", ParentID: "wf-root", Name: "new remote item", Priority: 3, Data: wf.NodeData{LayoutMode: "bullets"}})

	pull(t, m, wfIt)

	names := []string{}
	for _, c := range wfIt.children {
		names = append(names, c.name)
	}
	want := []string{"ship the API v2", "new remote item", "my local note"}
	if len(names) != 3 || names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Fatalf("reconciled children = %v, want %v", names, want)
	}
	// the renamed node kept its uuid (updated in place, not recreated)
	if wfIt.children[0].uuid != aUUID {
		t.Error("renamed mirror must keep its node uuid")
	}
	// stale mirror's map row is gone; new one exists
	saved, _ := database.AllWFNodes(m.db)
	for uuid, id := range saved {
		if id == "wf-b" {
			t.Errorf("stale wf-b mapping survived on %s", uuid)
		}
	}
	if len(saved) != 4 { // root, a, a1, c
		t.Fatalf("want 4 map rows after reconcile, got %d", len(saved))
	}
}

func TestWFChildRefreshesOwnBranch(t *testing.T) {
	srv := newWFMock(t, wfForest())
	m, wfIt := newWFTestModel(t, srv)
	pull(t, m, wfIt)

	a := wfIt.children[1] // "ship the API"
	srv.set(wf.Node{ID: "wf-a1", ParentID: "wf-a", Name: "openapi spec", Priority: 1, Data: wf.NodeData{LayoutMode: "bullets"}})

	// alt+r on the pulled child pulls just that branch (recursive mirror)
	pull(t, m, a)
	if len(a.children) != 1 || a.children[0].name != "openapi spec" {
		t.Fatalf("branch refresh failed: %+v", a.children)
	}
}

func TestWFNoKeyFlashes(t *testing.T) {
	srv := newWFMock(t, wfForest())
	m, wfIt := newWFTestModel(t, srv)
	m.wfClient = &wf.Client{APIKey: ""} // no key configured
	if cmd := runWF(m, wfIt); cmd != nil {
		t.Fatal("runWF without a key must not fire")
	}
	if m.flash == "" {
		t.Fatal("missing key must explain itself in the status bar")
	}
}
