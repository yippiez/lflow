package wf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// mockServer builds an httptest server speaking the Workflowy nodes API over a
// fixed forest: map of parent id → ordered children, plus a by-id index. It also
// asserts the Bearer key on every request.
func mockServer(t *testing.T, nodes []Node) *httptest.Server {
	t.Helper()
	byID := map[string]Node{}
	byParent := map[string][]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
		byParent[n.ParentID] = append(byParent[n.ParentID], n)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		pid := r.URL.Query().Get("parent_id")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"nodes": byParent[pid]})
	})
	mux.HandleFunc("/nodes/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		id := r.URL.Path[len("/nodes/"):]
		n, ok := byID[id]
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(n)
	})
	return httptest.NewServer(mux)
}

func testForest() []Node {
	done := int64(1753120900)
	return []Node{
		{ID: "root-1", Name: "Project <b>plan</b>", Note: "the big one", Priority: 1, Data: NodeData{LayoutMode: "h1"}},
		{ID: "a", ParentID: "root-1", Name: "ship the API", Priority: 2, Data: NodeData{LayoutMode: "todo"}},
		{ID: "b", ParentID: "root-1", Name: "write docs", Priority: 1, Data: NodeData{LayoutMode: "todo"}, CompletedAt: &done},
		{ID: "c", ParentID: "root-1", Name: "print(&quot;hello&quot;)", Priority: 3, Data: NodeData{LayoutMode: "code-block"}},
		{ID: "a1", ParentID: "a", Name: "deep <i>child</i>", Priority: 1, Data: NodeData{LayoutMode: "bullets"}},
	}
}

func testClient(srv *httptest.Server) *Client {
	return &Client{APIKey: "test-key", BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestFetchSubtree(t *testing.T) {
	srv := mockServer(t, testForest())
	defer srv.Close()

	root, truncated, err := testClient(srv).FetchSubtree(context.Background(), "root-1")
	if err != nil {
		t.Fatalf("FetchSubtree: %v", err)
	}
	if truncated {
		t.Fatal("small forest must not truncate")
	}
	if root.Name != "Project <b>plan</b>" || len(root.Children) != 3 {
		t.Fatalf("root wrong: name=%q children=%d", root.Name, len(root.Children))
	}
	// children sorted by priority: b(1), a(2), c(3)
	order := []string{"b", "a", "c"}
	for i, want := range order {
		if root.Children[i].ID != want {
			t.Errorf("child %d = %s, want %s (priority order)", i, root.Children[i].ID, want)
		}
	}
	// recursion reaches a's child
	var a *TreeNode
	for _, c := range root.Children {
		if c.ID == "a" {
			a = c
		}
	}
	if a == nil || len(a.Children) != 1 || a.Children[0].ID != "a1" {
		t.Fatal("nested child a1 not fetched")
	}
}

func TestFetchSubtreeAuthError(t *testing.T) {
	srv := mockServer(t, testForest())
	defer srv.Close()

	c := &Client{APIKey: "wrong", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, _, err := c.FetchSubtree(context.Background(), "root-1"); err == nil {
		t.Fatal("bad key must surface an error")
	}
}

func TestFetchSubtreeCap(t *testing.T) {
	// a flat parent with MaxFetch+50 children must truncate, not spiral
	nodes := []Node{{ID: "big", Name: "big", Data: NodeData{LayoutMode: "bullets"}}}
	for i := 0; i < MaxFetch+50; i++ {
		nodes = append(nodes, Node{
			ID: fmt.Sprintf("k%d", i), ParentID: "big",
			Name: fmt.Sprintf("kid %d", i), Priority: float64(i),
			Data: NodeData{LayoutMode: "bullets"},
		})
	}
	srv := mockServer(t, nodes)
	defer srv.Close()

	root, truncated, err := testClient(srv).FetchSubtree(context.Background(), "big")
	if err != nil {
		t.Fatalf("FetchSubtree: %v", err)
	}
	if !truncated {
		t.Fatal("oversized subtree must report truncation")
	}
	if len(root.Children) >= MaxFetch {
		t.Fatalf("children fetched past the cap: %d", len(root.Children))
	}
}

func TestTypeForAndPlainName(t *testing.T) {
	cases := []struct {
		layout, wantType string
	}{
		{"bullets", database.TypeBullets}, {"todo", database.TypeTodo},
		{"h1", database.TypeH1}, {"h2", database.TypeH2}, {"h3", database.TypeH3},
		{"code-block", database.TypeCode}, {"quote-block", database.TypeQuote},
		{"someday-mode", database.TypeBullets}, {"", database.TypeBullets},
	}
	for _, c := range cases {
		if got := TypeFor(Node{Data: NodeData{LayoutMode: c.layout}}); got != c.wantType {
			t.Errorf("TypeFor(%q) = %q, want %q", c.layout, got, c.wantType)
		}
	}

	names := map[string]string{
		"Project <b>plan</b>":                      "Project plan",
		"a <a href=\"https://x.co\">link</a> here": "a link here",
		"print(&quot;hello&quot;)":                 "print(\"hello\")",
		"no markup":                                "no markup",
		"<s>done</s> &amp; dusted":                 "done & dusted",
	}
	for in, want := range names {
		if got := PlainName(in); got != want {
			t.Errorf("PlainName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRef(t *testing.T) {
	cases := map[string]string{
		"ee1ac4c4-775e-1983-ae98-a8eeb92b1aca":                 "ee1ac4c4-775e-1983-ae98-a8eeb92b1aca",
		"https://workflowy.com/#/abc123def456":                 "abc123def456",
		"pull https://workflowy.com/#/8f2f00c7f7b1 into notes": "8f2f00c7f7b1",
		"wf ee1ac4c4-775e-1983-ae98-a8eeb92b1aca trailing":     "ee1ac4c4-775e-1983-ae98-a8eeb92b1aca",
	}
	for in, want := range cases {
		got, ok := ParseRef(in)
		if !ok || got != want {
			t.Errorf("ParseRef(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "just words", "#tag only"} {
		if _, ok := ParseRef(bad); ok {
			t.Errorf("ParseRef(%q) must not match", bad)
		}
	}
}

func TestLoadAPIKey(t *testing.T) {
	dir := t.TempDir()
	if got := LoadAPIKey(dir); got != "" {
		t.Errorf("missing file must yield empty key, got %q", got)
	}
	sub := filepath.Join(dir, "lflow")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "credentials.json"),
		[]byte(`{"workflowy":{"api_key":"wk-123"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadAPIKey(dir); got != "wk-123" {
		t.Errorf("LoadAPIKey = %q, want wk-123", got)
	}
}
