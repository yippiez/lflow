package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// newTestAPI spins a store on a throwaway DB and returns the HTTP mux plus a
// tiny JSON request helper.
func newTestAPI(t *testing.T) (*httptest.Server, *Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.DB().Exec(database.GetDefaultSchemaSQL()); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureRoot(store.DB()); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureTemp(store.DB()); err != nil {
		t.Fatal(err)
	}
	sv := &server{store: store, httpDone: make(chan struct{})}
	hs := &httpServer{sv: sv}
	ts := httptest.NewServer(hs.mux())
	t.Cleanup(ts.Close)
	return ts, store
}

func doJSON(t *testing.T, method, url, body string, out any) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Lflow-Instance", "test")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("decoding %s %s: %v", method, url, err)
		}
	}
	return res
}

func TestHTTP_CreatePatchOutlineDelete(t *testing.T) {
	ts, _ := newTestAPI(t)

	var n database.Node
	res := doJSON(t, "POST", ts.URL+"/api/nodes", `{"parent_uuid":"root","name":"hello #tag"}`, &n)
	if res.StatusCode != 200 || n.UUID == "" || n.ParentUUID != database.RootUUID {
		t.Fatalf("create: status %d node %+v", res.StatusCode, n)
	}

	var patched database.Node
	doJSON(t, "PATCH", ts.URL+"/api/nodes/"+n.UUID, `{"name":"renamed","starred":true,"completed":true}`, &patched)
	if patched.Name != "renamed" || !patched.Starred || patched.CompletedAt == 0 {
		t.Fatalf("patch: %+v", patched)
	}

	var outline struct {
		Root  string          `json:"root"`
		Nodes []database.Node `json:"nodes"`
	}
	doJSON(t, "GET", ts.URL+"/api/outline", "", &outline)
	found := false
	for _, o := range outline.Nodes {
		if o.UUID == database.TempUUID {
			t.Fatal("outline must exclude the Temporary Domain")
		}
		if o.UUID == n.UUID {
			found = true
		}
	}
	if outline.Root != database.RootUUID || !found {
		t.Fatalf("outline: root=%q found=%v", outline.Root, found)
	}

	var del struct {
		Deleted int `json:"deleted"`
	}
	doJSON(t, "DELETE", ts.URL+"/api/nodes/"+n.UUID, "", &del)
	if del.Deleted != 1 {
		t.Fatalf("delete: %+v", del)
	}
	res = doJSON(t, "GET", ts.URL+"/api/nodes/"+n.UUID, "", nil)
	if res.StatusCode == 200 {
		// tombstoned rows still exist; the outline must hide them instead
		doJSON(t, "GET", ts.URL+"/api/outline", "", &outline)
		for _, o := range outline.Nodes {
			if o.UUID == n.UUID {
				t.Fatal("deleted node still in outline")
			}
		}
	}
}

func TestHTTP_MoveAndRanks(t *testing.T) {
	ts, _ := newTestAPI(t)

	mk := func(name string) database.Node {
		var n database.Node
		doJSON(t, "POST", ts.URL+"/api/nodes", `{"parent_uuid":"root","name":"`+name+`"}`, &n)
		return n
	}
	a, b, c := mk("a"), mk("b"), mk("c")

	// c after a → order a, c, b
	doJSON(t, "POST", ts.URL+"/api/nodes/"+c.UUID+"/move", `{"parent_uuid":"root","after":"`+a.UUID+`"}`, nil)
	var outline struct {
		Nodes []database.Node `json:"nodes"`
	}
	doJSON(t, "GET", ts.URL+"/api/outline", "", &outline)
	rank := map[string]int{}
	for _, o := range outline.Nodes {
		rank[o.UUID] = o.Rank
	}
	if !(rank[a.UUID] < rank[c.UUID] && rank[c.UUID] < rank[b.UUID]) {
		t.Fatalf("ranks after move: a=%d c=%d b=%d", rank[a.UUID], rank[c.UUID], rank[b.UUID])
	}

	// cycle guard: a under its own child must fail
	var child database.Node
	doJSON(t, "POST", ts.URL+"/api/nodes", `{"parent_uuid":"`+a.UUID+`","name":"kid"}`, &child)
	res := doJSON(t, "POST", ts.URL+"/api/nodes/"+a.UUID+"/move", `{"parent_uuid":"`+child.UUID+`"}`, nil)
	if res.StatusCode != 400 {
		t.Fatalf("cycle move: status %d", res.StatusCode)
	}
}

func TestHTTP_FreeTypeStrings(t *testing.T) {
	ts, _ := newTestAPI(t)

	var n database.Node
	res := doJSON(t, "POST", ts.URL+"/api/nodes", `{"parent_uuid":"root","name":"5","type":"counter"}`, &n)
	if res.StatusCode != 200 || n.Type != "counter" {
		t.Fatalf("custom type create: status %d %+v", res.StatusCode, n)
	}
	res = doJSON(t, "POST", ts.URL+"/api/nodes", `{"parent_uuid":"root","type":"Bad Type!"}`, nil)
	if res.StatusCode != 400 {
		t.Fatalf("bad type accepted: status %d", res.StatusCode)
	}
}

func TestHTTP_Token(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(database.GetDefaultSchemaSQL()); err != nil {
		t.Fatal(err)
	}
	sv := &server{store: store, httpDone: make(chan struct{})}
	hs := &httpServer{sv: sv, token: "secret"}
	ts := httptest.NewServer(hs.mux())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d", res.StatusCode)
	}
	res, err = http.Get(ts.URL + "/api/info?token=secret")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("query token: status %d", res.StatusCode)
	}
}
