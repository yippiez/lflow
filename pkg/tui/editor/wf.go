package editor

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/wf"
)

// The wf node type: a Workflowy mirror. The node's text holds a pasted
// Workflowy link (or bare node id); alt+r pulls that subtree through the API
// and reconciles it in place as readonly children, each bound to its Workflowy
// id in the wf_nodes table. Every pulled node is itself a wf mirror — alt+r on
// any of them refreshes just that branch (the recursive-mirror model). The
// integration is read-only today; the id map is the two-way hook for later.

// wfGlyph is the mirror mark: ◈ in the accent color, the live diamond that
// marks a Workflowy-backed node.
func wfGlyph(it *item) (string, string) {
	return "◈", cAccent
}

// wfDoneMsg lands a finished pull (or its error) back in the update loop.
type wfDoneMsg struct {
	uuid      string // the pull root's node uuid
	root      *wf.TreeNode
	truncated bool
	err       error
}

// wfIDFor resolves the Workflowy id a node refreshes from: its own mapping row
// (any pulled node), falling back to an id pasted in its text (a fresh wf root).
func (m *Model) wfIDFor(it *item) (string, bool) {
	if id, ok := m.wfMap[it.uuid]; ok && id != "" {
		return id, true
	}
	return wf.ParseRef(database.ExpandAnchors(it.name, m.chips))
}

// wfEnsureClient builds the API client from credentials.json on first use.
// Tests inject m.wfClient directly; LFLOW_WF_BASE_URL points the client at a
// local mock service for demos and manual testing.
func (m *Model) wfEnsureClient() *wf.Client {
	if m.wfClient == nil {
		key := ""
		if m.ctx.Paths.Config != "" {
			key = wf.LoadAPIKey(m.ctx.Paths.Config)
		}
		m.wfClient = &wf.Client{APIKey: key, BaseURL: os.Getenv("LFLOW_WF_BASE_URL")}
	}
	return m.wfClient
}

// runWF starts a pull for the node under the cursor. Never auto-runs — alt+r
// only, like every runnable type.
func runWF(m *Model, it *item) tea.Cmd {
	id, ok := m.wfIDFor(it)
	if !ok {
		m.flash = "wf · paste a workflowy link or node id in this node first"
		return nil
	}
	client := m.wfEnsureClient()
	if client.APIKey == "" {
		m.flash = "wf · no api key — add {\"workflowy\":{\"api_key\":\"…\"}} to ~/.config/lflow/credentials.json"
		return nil
	}
	if m.wfBusy == nil {
		m.wfBusy = map[string]bool{}
	}
	if m.wfBusy[it.uuid] {
		return nil
	}
	m.wfBusy[it.uuid] = true
	m.flash = "wf · pulling…"
	uuid := it.uuid
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		root, truncated, err := client.FetchSubtree(ctx, id)
		return wfDoneMsg{uuid: uuid, root: root, truncated: truncated, err: err}
	}
}

// handleWFDone reconciles a finished pull into the outline.
func (m *Model) handleWFDone(msg wfDoneMsg) {
	delete(m.wfBusy, msg.uuid)
	it := m.tree.byUUID[msg.uuid]
	if it == nil {
		return // the pull root was deleted mid-flight
	}
	if msg.err != nil {
		m.flash = "wf · " + msg.err.Error()
		return
	}
	m.pushUndo("")
	n := m.reconcileWF(it, msg.root)
	m.unsaved = true
	m.refreshRows()
	m.flash = fmt.Sprintf("wf · pulled %d nodes", n)
	if msg.truncated {
		m.flash += fmt.Sprintf(" (truncated at %d)", wf.MaxFetch)
	}
}

// reconcileWF applies one fetched Workflowy node to root and recursively
// reconciles its children: existing mirrors (matched by Workflowy id) update in
// place, new nodes are created readonly, stale mirrors tombstone, and the
// user's own non-mirror children survive untouched after the mirrored run.
// Returns how many Workflowy nodes were applied.
func (m *Model) reconcileWF(root *item, wn *wf.TreeNode) int {
	m.applyWFNode(root, wn, root.typ == database.TypeWF)
	count := 1

	if m.wfMap == nil {
		m.wfMap = map[string]string{}
	}

	// index existing mirror children by their workflowy id
	existing := map[string]*item{}
	var others []*item
	for _, c := range root.children {
		id, mapped := m.wfMap[c.uuid]
		if !mapped {
			others = append(others, c)
			continue
		}
		if _, dup := existing[id]; dup {
			m.dropWFSubtree(c)
			continue
		}
		existing[id] = c
	}

	var kids []*item
	for _, cw := range wn.Children {
		child, ok := existing[cw.ID]
		if ok {
			delete(existing, cw.ID)
		} else {
			nc, err := m.tree.newItem()
			if err != nil {
				break
			}
			child = nc
			child.parent = root
			m.bindWF(child, cw.ID)
		}
		kids = append(kids, child)
		count += m.reconcileWF(child, cw)
	}
	// mirrors whose source vanished from Workflowy
	for _, gone := range existing {
		m.dropWFSubtree(gone)
	}
	root.children = append(kids, others...)
	return count
}

// applyWFNode copies one Workflowy node's fields onto an item. The pull root
// keeps its wf type (it stays the refresh handle the user made); pulled
// children wear the translated type so todos check and headings size, and are
// readonly — this is a read-only mirror.
func (m *Model) applyWFNode(it *item, wn *wf.TreeNode, keepType bool) {
	it.name = wf.PlainName(wn.Name)
	it.note = wf.PlainName(wn.Note)
	if !keepType {
		it.typ = wf.TypeFor(wn.Node)
		it.readonly = true
	}
	if wn.CompletedAt != nil {
		it.completedAt = *wn.CompletedAt * int64(time.Second)
	} else {
		it.completedAt = 0
	}
	m.bindWF(it, wn.ID)
	m.backfillName(it) // #tags / dates in pulled text become chips like anywhere
}

// bindWF records the node ↔ workflowy id mapping in memory and in the DB.
func (m *Model) bindWF(it *item, wfID string) {
	if m.wfMap == nil {
		m.wfMap = map[string]string{}
	}
	m.wfMap[it.uuid] = wfID
	if m.db != nil {
		_ = database.UpsertWFNode(m.db, it.uuid, wfID, time.Now().UnixNano())
	}
}

// dropWFSubtree tombstones a stale mirror node and its subtree, releasing every
// mapping row underneath.
func (m *Model) dropWFSubtree(it *item) {
	for _, c := range it.children {
		m.dropWFSubtree(c)
	}
	delete(m.wfMap, it.uuid)
	if m.db != nil {
		_ = database.DeleteWFNode(m.db, it.uuid)
	}
	m.tombstoneItem(it)
}
