package editor

import (
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/crypto"
	"github.com/lflow/lflow/tui/database"
)

// The Encrypted Query node — Query's counterpart for vaults, and the reason it
// has to exist at all.
//
// TypeQuery searches the nodes table. A vault's contents were never in the
// nodes table: they are ciphertext in a blob, and the ordinary query's failure
// to find them is not an oversight to patch but the encryption working. So a
// search that reaches inside a vault cannot be a filter — it has to OPEN one.
// That is the whole shape of this type:
//
//	alt+r  →  gather every vault in the outline
//	          · already open in this session → search its live cleartext
//	          · sealed → try the key typed at the prompt; a vault that does not
//	            open is counted, never named as a failure to guess again
//	       →  materialize the hits as EPHEMERAL rows
//
// The hits are ephemeral for the same reason the vault children are: a Query
// node writes its results down as real mirror rows, and doing that here would
// spill a vault's cleartext into the table one search at a time. So this node's
// entire subtree is unpersisted — nothing typed under it is written down
// either. Re-running rebuilds it; quitting loses it; that is correct.
//
// The query language is the same one (see querytime.go): text, "quoted"
// semantic phrases, #tags, :type:, && || and > all work, because the matcher is
// handed a candidate set built the same way — just built out of decrypted
// content instead of database rows.
func init() {
	registerType(nodeType{
		key:            database.TypeEncQuery,
		label:          "Encrypted Query",
		inlineEditable: true,
		disableChips:   true,
		prefix:         encQueryPrefix,
		spanColor:      querySpanColor,
		baseColor:      func(*item) string { return "" },
		bodyTail:       encQueryTail,
		run:            runEncQuery,
		flashActions: func(m *Model, it *item) []flashAction {
			return []flashAction{{verb: "search", color: cGreen, do: runEncQuery}}
		},
	})
}

// encQueryPrefix is the query node's ⌕ wearing the vault's hatching, so the two
// search types are told apart at a glance in a long outline.
func encQueryPrefix(*item) string { return cYellow + "⌕" + glyphVaultLocked + cReset + " " }

// encQueryTail reports the last run: how many vaults answered, and how many
// stayed shut. The shut count matters — without it an empty result reads as
// "nothing matched" when it may mean "nothing opened".
func encQueryTail(it *item, _ map[string]database.Chip) string {
	d, ok := it.encQueryStats()
	if !ok {
		return cDim + " · alt+r searches your vaults" + cReset
	}
	tail := cDim + " · " + strconv.Itoa(d.hits) + " " + plural(d.hits, "hit") +
		" in " + strconv.Itoa(d.opened) + " " + plural(d.opened, "vault") + cReset
	if d.sealed > 0 {
		tail += cRed + " · " + strconv.Itoa(d.sealed) + " still sealed" + cReset
	}
	return tail
}

// encQueryRun is one run's tally, kept on the item so the row can render it
// without the Model. It is a value, not a pointer, so a stale read is a stale
// number and never a nil dereference.
type encQueryRun struct {
	hits, opened, sealed int
}

// encQueryRuns is each query's last tally, published by the Model for the
// Model-less render path — the same arrangement as agentLooks, and for the same
// reason: bodyTail is handed an item, not a Model. Ephemeral either way; a
// tally is a count of hits, never their text.
var encQueryRuns = map[string]encQueryRun{}

func (it *item) encQueryStats() (encQueryRun, bool) {
	d, ok := encQueryRuns[it.uuid]
	return d, ok
}

// encQueryMaxHits bounds the result view the same way the Query node's does. An
// unbounded fan-out of decrypted rows is a worse outcome here than there.
const encQueryMaxHits = 50

// runEncQuery is alt+r. If any vault in the outline is still sealed it asks for
// a key first — one prompt tried against all of them, because a person's vaults
// usually share a password and asking once per vault would be unusable.
func runEncQuery(m *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	raw := strings.TrimSpace(database.ExpandAnchors(it.name, m.chips))
	if raw == "" {
		m.errorFlash("encrypted query: type what to search for")
		return nil
	}
	// One prompt, tried against every sealed vault. Asking per vault would be
	// unusable at any real number of them, and vaults belonging to one person
	// usually share a key; the ones it does not open are simply counted.
	if m.sealedVaultCount() > 0 {
		m.openVaultKey(it, vaultKeySearch)
		return nil
	}
	m.searchVaults(it, crypto.Secrets{})
	return m.scheduleSync()
}

// sealedVaultCount is how many vaults this session cannot currently read.
func (m *Model) sealedVaultCount() int {
	n := 0
	for _, uuid := range m.vaultUUIDs() {
		if !m.vaultOpen(uuid) {
			n++
		}
	}
	return n
}

// vaultCorpus is one vault's cleartext, ready to search.
type vaultCorpus struct {
	uuid    string
	content crypto.Content
}

// searchVaults opens what it can, searches it, and materializes the hits.
// Secrets are the key to try against vaults this session has not already
// opened; the zero value means "only the ones already open".
func (m *Model) searchVaults(q *item, s crypto.Secrets) {
	corpora, sealed := m.gatherVaults(s)
	raw := strings.TrimSpace(database.ExpandAnchors(q.name, m.chips))
	pq := parseQuery(raw, time.Now())
	if pq.empty() {
		m.errorFlash("encrypted query: " + raw + " matched nothing to search for")
		return
	}

	ctx, owner := encQueryCtx(m, corpora)
	matches := evalMatches(q.uuid, pq, database.RootUUID, ctx)
	if len(matches) > encQueryMaxHits {
		matches = matches[:encQueryMaxHits]
	}

	m.dropVaultChildren(m.tree, q) // the previous run's cleartext leaves memory first
	for _, n := range matches {
		m.appendEncQueryHit(q, n, owner[n.UUID])
	}
	encQueryRuns[q.uuid] = encQueryRun{hits: len(matches), opened: len(corpora), sealed: sealed}
	searched := 0
	for _, c := range corpora {
		searched += c.content.CountNodes()
	}
	m.flash = "searched " + strconv.Itoa(searched) + " " + plural(searched, "item") +
		" in " + strconv.Itoa(len(corpora)) + " " + plural(len(corpora), "vault")
	m.refreshRows()
}

// gatherVaults collects the cleartext of every vault it can read: the ones this
// session already holds keys for (read live from the tree, so unsaved edits are
// searchable) and any sealed one the supplied key happens to open. It returns
// the corpora and the count that stayed shut.
func (m *Model) gatherVaults(s crypto.Secrets) (corpora []vaultCorpus, sealed int) {
	for _, uuid := range m.vaultUUIDs() {
		if it := m.vaultItem(uuid); it != nil && m.vaultOpen(uuid) {
			corpora = append(corpora, vaultCorpus{uuid: uuid, content: m.vaultContentOf(it)})
			continue
		}
		env, err := m.vaultEnvelope(uuid)
		if err != nil || env == nil {
			sealed++
			continue
		}
		plain, _, err := env.Open(s)
		if err != nil {
			// Which vault refused and why is not reported: a per-vault error
			// message is an oracle telling an attacker which password was close.
			sealed++
			continue
		}
		content, err := crypto.UnmarshalContent(plain)
		if err != nil {
			sealed++
			continue
		}
		corpora = append(corpora, vaultCorpus{uuid: uuid, content: content})
	}
	return corpora, sealed
}

// vaultTrees is where a vault item can be found. The stash matters: inside the
// Temporary Domain m.tree IS the scratch tree, and a search run from there
// would otherwise see none of the outline's vaults — and, worse, would treat an
// OPEN one as sealed and ask for a key it already holds.
func (m *Model) vaultTrees() []*tree { return []*tree{m.tree, m.mainStash.tree} }

// vaultItem finds a vault's live item in whichever tree currently holds it.
func (m *Model) vaultItem(uuid string) *item {
	for _, t := range m.vaultTrees() {
		if t == nil {
			continue
		}
		if it := t.byUUID[uuid]; it != nil {
			return it
		}
	}
	return nil
}

// vaultUUIDs is every encrypted node worth trying, in a stable order: the ones
// in the loaded tree plus every other one in the outline, so a search covers
// vaults that are not currently on screen.
func (m *Model) vaultUUIDs() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range m.vaultTrees() {
		if t == nil {
			continue
		}
		for uuid, it := range t.byUUID {
			if it.typ == database.TypeEncrypted && !seen[uuid] {
				seen[uuid] = true
				out = append(out, uuid)
			}
		}
	}
	if m.db != nil {
		_ = database.StreamLiveNodes(m.db, 500, func(nodes []database.Node) bool {
			for _, n := range nodes {
				if n.Type == database.TypeEncrypted && !seen[n.UUID] {
					seen[n.UUID] = true
					out = append(out, n.UUID)
				}
			}
			return true
		})
	}
	sort.Strings(out) // a stable order keeps the result rows from reshuffling
	return out
}

// encQueryCtx builds the matcher's candidate universe out of decrypted content.
// Each vault node gets a synthetic id — it has no uuid of its own and must
// never be given one that could be written down — parented up through the
// vault's own row to the forest root, which is what lets `>` chains and scoping
// behave exactly as they do over real nodes. owner maps each synthetic id back
// to the vault it came from, for the hit's breadcrumb.
func encQueryCtx(m *Model, corpora []vaultCorpus) (*qCtx, map[string]string) {
	ctx := &qCtx{m: m, now: time.Now(), chips: m.chipSnapshot(),
		parent: map[string]string{}, byUUID: map[string]*qCand{}, seen: map[string]bool{}}
	owner := map[string]string{}

	for _, c := range corpora {
		// the vault's own row anchors the chain; it is not itself a candidate
		// (its name is the vault's title, which the user can already see)
		ctx.parent[c.uuid] = database.RootUUID
		ids := map[string]string{} // path key → synthetic id
		n := 0
		c.content.Walk(func(node crypto.Node, path crypto.Path) {
			n++
			id := c.uuid + "/" + strconv.Itoa(n)
			key := strings.Join(append(path, node.Name), "\x00")
			parentKey := strings.Join(path, "\x00")
			parent := c.uuid
			if p, ok := ids[parentKey]; ok {
				parent = p
			}
			ids[key] = id
			owner[id] = c.uuid
			ctx.add(nil, qCand{uuid: id, name: node.Name, note: node.Note, typ: node.Type,
				parent: parent, starred: node.Starred, completedAt: node.CompletedAt, style: node.Style})
		})
	}
	return ctx, owner
}

// appendEncQueryHit hangs one decrypted match under the query as an ephemeral,
// read-only row. It is NOT a mirror: a mirror points at a uuid, and this row
// stands for something that has no uuid anywhere. It carries its vault's title
// as a breadcrumb so a hit says where it came from.
func (m *Model) appendEncQueryHit(q *item, n database.Node, vaultUUID string) {
	it, err := m.tree.newItem()
	if err != nil {
		m.errorFlash("encrypted query: " + err.Error())
		return
	}
	it.parent = q
	it.ephemeral = true
	it.structureLocked = true // a generated row cannot be moved out of the view
	it.name = n.Name
	it.note = n.Note
	it.typ = n.Type
	if it.typ == "" {
		it.typ = database.TypeBullets
	}
	it.style = n.Style
	if v := m.vaultItem(vaultUUID); v != nil {
		m.nodeStore(it.uuid)["encQueryVault"] = v.name
	}
	q.children = append(q.children, it)
}
