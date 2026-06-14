package wf_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/utils"
	"github.com/lflow/lflow/pkg/cli/wf"
)

// fakeItem mirrors the wfItem wire format the client expects.
type fakeItem struct {
	id       string
	name     string
	note     string
	cp       int64 // nonzero when completed
	lm       int64
	children []*fakeItem
}

// fakeServer is an in-memory mutable workflowy tree exposed over HTTP.
type fakeServer struct {
	t      *testing.T
	roots  []*fakeItem
	txN    int
	server *httptest.Server
}

func newFakeServer(t *testing.T) *fakeServer {
	fs := &fakeServer{t: t, txN: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("/get_initialization_data", fs.handleInit)
	mux.HandleFunc("/push_and_poll", fs.handlePush)
	fs.server = httptest.NewServer(mux)
	t.Cleanup(fs.server.Close)
	return fs
}

func (fs *fakeServer) URL() string { return fs.server.URL }

func (fs *fakeServer) client() *wf.InternalClient {
	return wf.NewInternalClient(fs.URL(), "test-session")
}

// find locates an item anywhere in the tree by id, returning the item and its
// parent's child-slice pointer (for removal).
func (fs *fakeServer) find(id string) (*fakeItem, *[]*fakeItem) {
	var walk func(list *[]*fakeItem) (*fakeItem, *[]*fakeItem)
	walk = func(list *[]*fakeItem) (*fakeItem, *[]*fakeItem) {
		for _, it := range *list {
			if it.id == id {
				return it, list
			}
			if hit, parent := walk(&it.children); hit != nil {
				return hit, parent
			}
		}
		return nil, nil
	}
	return walk(&fs.roots)
}

func wireItem(it *fakeItem) map[string]interface{} {
	ch := make([]map[string]interface{}, 0, len(it.children))
	for _, c := range it.children {
		ch = append(ch, wireItem(c))
	}
	return map[string]interface{}{
		"id": it.id,
		"nm": it.name,
		"no": it.note,
		"cp": it.cp,
		"lm": it.lm,
		"ch": ch,
	}
}

func (fs *fakeServer) handleInit(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("client_version") != "21" {
		fs.t.Errorf("unexpected client_version %q", r.URL.Query().Get("client_version"))
	}
	roots := make([]map[string]interface{}, 0, len(fs.roots))
	for _, it := range fs.roots {
		roots = append(roots, wireItem(it))
	}
	resp := map[string]interface{}{
		"projectTreeData": map[string]interface{}{
			"mainProjectTreeInfo": map[string]interface{}{
				"rootProjectChildren":                     roots,
				"initialMostRecentOperationTransactionId": fmt.Sprintf("tx-%d", fs.txN),
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type wireOp struct {
	Type string `json:"type"`
	Data struct {
		ProjectID   string `json:"projectid"`
		ParentID    string `json:"parentid"`
		Priority    int    `json:"priority"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"data"`
}

type wirePush struct {
	Operations []wireOp `json:"operations"`
}

func (fs *fakeServer) handlePush(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fs.t.Fatalf("parsing push form: %v", err)
	}
	raw := r.FormValue("push_poll_data")
	var pushes []wirePush
	if err := json.Unmarshal([]byte(raw), &pushes); err != nil {
		fs.t.Fatalf("decoding push_poll_data %q: %v", raw, err)
	}
	if len(pushes) > 0 {
		for _, op := range pushes[0].Operations {
			fs.apply(op)
		}
	}

	fs.txN++
	resp := map[string]interface{}{
		"results": []map[string]interface{}{{
			"new_most_recent_operation_transaction_id": fmt.Sprintf("tx-%d", fs.txN),
			"error_encountered_in_remote_operations":   false,
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (fs *fakeServer) apply(op wireOp) {
	switch op.Type {
	case "create":
		child := &fakeItem{id: op.Data.ProjectID, lm: time.Now().UnixNano()}
		if op.Data.ParentID == "" || op.Data.ParentID == "None" {
			fs.roots = insertAt(fs.roots, child, op.Data.Priority)
			return
		}
		parent, _ := fs.find(op.Data.ParentID)
		if parent == nil {
			fs.t.Fatalf("create under unknown parent %q", op.Data.ParentID)
		}
		parent.children = insertAt(parent.children, child, op.Data.Priority)
	case "edit":
		it, _ := fs.find(op.Data.ProjectID)
		if it == nil {
			fs.t.Fatalf("edit of unknown node %q", op.Data.ProjectID)
		}
		it.name = op.Data.Name
		it.note = op.Data.Description
		it.lm = time.Now().UnixNano()
	case "delete":
		it, list := fs.find(op.Data.ProjectID)
		if it == nil {
			return
		}
		*list = removeItem(*list, it)
	case "complete":
		it, _ := fs.find(op.Data.ProjectID)
		if it == nil {
			fs.t.Fatalf("complete of unknown node %q", op.Data.ProjectID)
		}
		it.cp = time.Now().Unix()
	case "uncomplete":
		it, _ := fs.find(op.Data.ProjectID)
		if it == nil {
			fs.t.Fatalf("uncomplete of unknown node %q", op.Data.ProjectID)
		}
		it.cp = 0
	default:
		fs.t.Fatalf("unhandled op type %q", op.Type)
	}
}

func insertAt(list []*fakeItem, child *fakeItem, priority int) []*fakeItem {
	if priority < 0 || priority > len(list) {
		priority = len(list)
	}
	list = append(list, nil)
	copy(list[priority+1:], list[priority:])
	list[priority] = child
	return list
}

func removeItem(list []*fakeItem, target *fakeItem) []*fakeItem {
	out := list[:0]
	for _, it := range list {
		if it != target {
			out = append(out, it)
		}
	}
	return out
}

// --- test helpers ---------------------------------------------------------

// insertAnchor inserts a local anchor node and creates the wf mirror binding
// it to wfID. Returns the anchor uuid.
func insertAnchor(t *testing.T, db *database.DB, wfID string) string {
	t.Helper()
	uuid := mustUUID(t)
	now := time.Now().UnixNano()
	n := database.Node{
		UUID:     uuid,
		Name:     "anchor",
		Layout:   database.LayoutBullets,
		AddedOn:  now,
		EditedOn: now,
	}
	if err := n.Insert(db); err != nil {
		t.Fatalf("inserting anchor: %v", err)
	}
	if err := wf.CreateMirror(db, uuid, wfID); err != nil {
		t.Fatalf("creating mirror: %v", err)
	}
	return uuid
}

func mustUUID(t *testing.T) string {
	t.Helper()
	u, err := utils.GenerateUUID()
	if err != nil {
		t.Fatalf("generating uuid: %v", err)
	}
	return u
}

func newSyncer(t *testing.T, db *database.DB, fs *fakeServer) (*wf.Syncer, string) {
	t.Helper()
	journalPath := filepath.Join(t.TempDir(), "journal.tsv")
	return &wf.Syncer{
		DB:      db,
		Client:  fs.client(),
		Journal: wf.Journal{Path: journalPath},
	}, journalPath
}

// localChildByName returns the single non-deleted local child of parentUUID
// whose name matches, failing if none/many.
func localChildByName(t *testing.T, db *database.DB, parentUUID, name string) database.Node {
	t.Helper()
	children, err := database.GetChildren(db, parentUUID)
	if err != nil {
		t.Fatalf("getting children: %v", err)
	}
	for _, c := range children {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no child named %q under %s (have %v)", name, parentUUID, childNames(children))
	return database.Node{}
}

func childNames(children []database.Node) []string {
	var out []string
	for _, c := range children {
		out = append(out, c.Name)
	}
	return out
}

func mappingWfID(t *testing.T, db *database.DB, nodeUUID string) (string, bool) {
	t.Helper()
	var wfID string
	err := db.QueryRow("SELECT wf_id FROM wf_mirrors WHERE node_uuid = ?", nodeUUID).Scan(&wfID)
	if err != nil {
		return "", false
	}
	return wfID, true
}

func readJournal(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("reading journal: %v", err)
	}
	return string(data)
}

// --- scenarios ------------------------------------------------------------

// a. Initial pull of a 2-level nested wf subtree.
func TestSyncInitialPull(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	fs := newFakeServer(t)
	fs.roots = []*fakeItem{{
		id: "wf-anchor", name: "Anchor", lm: 100,
		children: []*fakeItem{
			{id: "wf-a", name: "Alpha", lm: 110, children: []*fakeItem{
				{id: "wf-a1", name: "Alpha-1", lm: 111},
				{id: "wf-a2", name: "Alpha-2", note: "n2", lm: 112},
			}},
			{id: "wf-b", name: "Beta", lm: 120},
		},
	}}

	anchor := insertAnchor(t, db, "wf-anchor")
	syncer, _ := newSyncer(t, db, fs)

	res, err := syncer.Sync(anchor, time.Now().Unix())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Pulled != 4 {
		t.Fatalf("Pulled = %d, want 4", res.Pulled)
	}

	// order under anchor
	top, err := database.GetChildren(db, anchor)
	if err != nil {
		t.Fatalf("children: %v", err)
	}
	if got := childNames(top); len(got) != 2 || got[0] != "Alpha" || got[1] != "Beta" {
		t.Fatalf("anchor children = %v, want [Alpha Beta]", got)
	}

	alpha := localChildByName(t, db, anchor, "Alpha")
	subs, _ := database.GetChildren(db, alpha.UUID)
	if got := childNames(subs); len(got) != 2 || got[0] != "Alpha-1" || got[1] != "Alpha-2" {
		t.Fatalf("Alpha children = %v, want [Alpha-1 Alpha-2]", got)
	}
	a2 := localChildByName(t, db, alpha.UUID, "Alpha-2")
	if a2.Note != "n2" {
		t.Fatalf("Alpha-2 note = %q, want n2", a2.Note)
	}
	if a2.ParentUUID != alpha.UUID {
		t.Fatalf("Alpha-2 parent = %s, want %s", a2.ParentUUID, alpha.UUID)
	}

	// every pulled node has a mapping
	for _, wfID := range []string{"wf-a", "wf-a1", "wf-a2", "wf-b"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM wf_mirrors WHERE wf_id = ? AND anchor = ?", wfID, anchor).Scan(&count); err != nil {
			t.Fatalf("counting mapping: %v", err)
		}
		if count != 1 {
			t.Fatalf("mapping for %s = %d, want 1", wfID, count)
		}
	}
}

// b. A remote edit propagates to the local node on the next sync.
func TestSyncRemoteEdit(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	fs := newFakeServer(t)
	fs.roots = []*fakeItem{{
		id: "wf-anchor", name: "Anchor", lm: 100,
		children: []*fakeItem{{id: "wf-a", name: "Alpha", lm: 110}},
	}}
	anchor := insertAnchor(t, db, "wf-anchor")
	syncer, _ := newSyncer(t, db, fs)

	if _, err := syncer.Sync(anchor, time.Now().Unix()); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// bump nm + lm remotely
	it, _ := fs.find("wf-a")
	it.name = "Alpha renamed"
	it.lm = 200

	if _, err := syncer.Sync(anchor, time.Now().Unix()); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	localChildByName(t, db, anchor, "Alpha renamed") // fails if not renamed
}

// c. A new local node (no mapping) is pushed to workflowy and gets a mapping.
func TestSyncLocalCreatePushes(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	fs := newFakeServer(t)
	fs.roots = []*fakeItem{{id: "wf-anchor", name: "Anchor", lm: 100}}
	anchor := insertAnchor(t, db, "wf-anchor")
	syncer, _ := newSyncer(t, db, fs)

	// local-only child
	childUUID := mustUUID(t)
	now := time.Now().UnixNano()
	child := database.Node{
		UUID:       childUUID,
		ParentUUID: anchor,
		Name:       "Local child",
		Note:       "ln",
		Layout:     database.LayoutBullets,
		AddedOn:    now,
		EditedOn:   now,
	}
	if err := child.Insert(db); err != nil {
		t.Fatalf("inserting local child: %v", err)
	}

	res, err := syncer.Sync(anchor, time.Now().Unix())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Pushed != 1 {
		t.Fatalf("Pushed = %d, want 1", res.Pushed)
	}

	// fake tree now has it
	anchorItem, _ := fs.find("wf-anchor")
	if len(anchorItem.children) != 1 {
		t.Fatalf("wf anchor children = %d, want 1", len(anchorItem.children))
	}
	pushed := anchorItem.children[0]
	if pushed.name != "Local child" || pushed.note != "ln" {
		t.Fatalf("pushed item = %+v, want name=Local child note=ln", pushed)
	}

	// a mapping row was added
	wfID, ok := mappingWfID(t, db, childUUID)
	if !ok {
		t.Fatalf("no mapping added for local child")
	}
	if wfID != pushed.id {
		t.Fatalf("mapping wf_id = %s, want %s", wfID, pushed.id)
	}
}

// d. A local edit of a mapped node is pushed to workflowy.
func TestSyncLocalEditPushes(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	fs := newFakeServer(t)
	fs.roots = []*fakeItem{{
		id: "wf-anchor", name: "Anchor", lm: 100,
		children: []*fakeItem{{id: "wf-a", name: "Alpha", lm: 110}},
	}}
	anchor := insertAnchor(t, db, "wf-anchor")
	syncer, _ := newSyncer(t, db, fs)

	if _, err := syncer.Sync(anchor, time.Now().Unix()); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	alpha := localChildByName(t, db, anchor, "Alpha")
	// local edit newer than last_sync (stored edited_on)
	newEdited := time.Now().UnixNano() + int64(time.Hour)
	if _, err := db.Exec("UPDATE nodes SET name = ?, edited_on = ? WHERE uuid = ?",
		"Alpha local", newEdited, alpha.UUID); err != nil {
		t.Fatalf("local edit: %v", err)
	}

	res, err := syncer.Sync(anchor, time.Now().Unix())
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if res.Pushed != 1 {
		t.Fatalf("Pushed = %d, want 1", res.Pushed)
	}

	it, _ := fs.find("wf-a")
	if it.name != "Alpha local" {
		t.Fatalf("wf name = %q, want Alpha local", it.name)
	}
}

// e. Conflict: both sides change, workflowy wins, local loser journaled.
func TestSyncConflictWorkflowyWins(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	fs := newFakeServer(t)
	fs.roots = []*fakeItem{{
		id: "wf-anchor", name: "Anchor", lm: 100,
		children: []*fakeItem{{id: "wf-a", name: "Alpha", lm: 110}},
	}}
	anchor := insertAnchor(t, db, "wf-anchor")
	syncer, journalPath := newSyncer(t, db, fs)

	if _, err := syncer.Sync(anchor, time.Now().Unix()); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	alpha := localChildByName(t, db, anchor, "Alpha")

	// remote change
	it, _ := fs.find("wf-a")
	it.name = "Alpha from wf"
	it.lm = 300

	// local change too
	newEdited := time.Now().UnixNano() + int64(time.Hour)
	if _, err := db.Exec("UPDATE nodes SET name = ?, edited_on = ? WHERE uuid = ?",
		"Alpha local edit", newEdited, alpha.UUID); err != nil {
		t.Fatalf("local edit: %v", err)
	}

	res, err := syncer.Sync(anchor, time.Now().Unix())
	if err != nil {
		t.Fatalf("conflict sync: %v", err)
	}
	if res.Conflicts != 1 {
		t.Fatalf("Conflicts = %d, want 1", res.Conflicts)
	}

	updated, err := database.GetNode(db, alpha.UUID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if updated.Name != "Alpha from wf" {
		t.Fatalf("local name = %q, want Alpha from wf", updated.Name)
	}

	journal := readJournal(t, journalPath)
	if !strings.Contains(journal, "Alpha local edit") {
		t.Fatalf("journal missing overwritten local name; got:\n%s", journal)
	}
}

// f. A remote delete tombstones the local node and drops its mapping.
func TestSyncRemoteDelete(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	fs := newFakeServer(t)
	fs.roots = []*fakeItem{{
		id: "wf-anchor", name: "Anchor", lm: 100,
		children: []*fakeItem{
			{id: "wf-a", name: "Alpha", lm: 110},
			{id: "wf-b", name: "Beta", lm: 120},
		},
	}}
	anchor := insertAnchor(t, db, "wf-anchor")
	syncer, _ := newSyncer(t, db, fs)

	if _, err := syncer.Sync(anchor, time.Now().Unix()); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	alpha := localChildByName(t, db, anchor, "Alpha")

	// remove Alpha remotely
	anchorItem, _ := fs.find("wf-anchor")
	target, _ := fs.find("wf-a")
	anchorItem.children = removeItem(anchorItem.children, target)

	if _, err := syncer.Sync(anchor, time.Now().Unix()); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	deleted, err := database.GetNode(db, alpha.UUID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if !deleted.Deleted {
		t.Fatalf("Alpha not tombstoned")
	}

	if _, ok := mappingWfID(t, db, alpha.UUID); ok {
		t.Fatalf("mapping for deleted node still present")
	}
}

// g. Completed state propagates remote->local and local->remote.
func TestSyncCompletedBothWays(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	fs := newFakeServer(t)
	fs.roots = []*fakeItem{{
		id: "wf-anchor", name: "Anchor", lm: 100,
		children: []*fakeItem{{id: "wf-a", name: "Alpha", lm: 110}},
	}}
	anchor := insertAnchor(t, db, "wf-anchor")
	syncer, _ := newSyncer(t, db, fs)

	if _, err := syncer.Sync(anchor, time.Now().Unix()); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// remote completes Alpha
	it, _ := fs.find("wf-a")
	it.cp = time.Now().Unix()
	it.lm = 210

	// local-only completed node to push (complete op)
	doneUUID := mustUUID(t)
	now := time.Now().UnixNano()
	done := database.Node{
		UUID:        doneUUID,
		ParentUUID:  anchor,
		Name:        "Done task",
		Layout:      database.LayoutBullets,
		CompletedAt: time.Now().Unix(),
		AddedOn:     now,
		EditedOn:    now,
	}
	if err := done.Insert(db); err != nil {
		t.Fatalf("inserting done node: %v", err)
	}

	if _, err := syncer.Sync(anchor, time.Now().Unix()); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	// remote -> local: Alpha now completed locally
	alpha, err := database.GetNode(db, localChildByName(t, db, anchor, "Alpha").UUID)
	if err != nil {
		t.Fatalf("get alpha: %v", err)
	}
	if alpha.CompletedAt <= 0 {
		t.Fatalf("Alpha completed_at = %d, want > 0", alpha.CompletedAt)
	}

	// local -> remote: Done task pushed and completed in the fake tree
	wfID, ok := mappingWfID(t, db, doneUUID)
	if !ok {
		t.Fatalf("Done task not mapped")
	}
	pushed, _ := fs.find(wfID)
	if pushed == nil {
		t.Fatalf("Done task not present in fake tree")
	}
	if pushed.cp == 0 {
		t.Fatalf("Done task cp = 0 in fake tree, want completed")
	}
}
