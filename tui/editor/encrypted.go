package editor

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/crypto"
	"github.com/lflow/lflow/tui/database"
)

// The Encrypted node — a vault. Structurally it is a bullet and deliberately
// nothing more: it takes children of any type, imposes no meaning on them, and
// the outline inside it behaves like the outline outside it. What differs is
// where that outline LIVES.
//
// A vault's children are not rows. The nodes table holds one row for the vault
// itself, carrying nothing but garble (see crypto.Envelope.Garble); the real
// title, the note and the entire subtree are sealed into the node's blob and
// exist in the clear only in memory, only while this session holds the key. So:
//
//   - the row reads as noise everywhere — the outline, `lflow list`, an export,
//     a raw sqlite3 dump — because noise is all any of them was ever given;
//   - alt+e prompts for the key, and the subtree appears beneath the row;
//   - the ordinary Query node cannot find anything inside a vault, not because
//     it declines to look but because there is nothing indexed to find. That
//     absence is what the Encrypted Query node exists to answer (encquery.go).
//
// WARNING (invariant): a vault node's persisted name is garble and its
// persisted note is empty, ALWAYS. The cleartext title sits in it.name only
// while the vault is open, and sealVaults swaps the garble back before any save
// touches the database (see Model.saveAll). crypto.IsGarble on the name is
// therefore the Model-less render hooks' honest reading of "is this locked".
func init() {
	registerType(nodeType{
		key:            database.TypeEncrypted,
		label:          "Encrypted",
		inlineEditable: true, // the TITLE is editable — while open; see editTargetOf
		glyph:          vaultGlyph,
		baseColor:      vaultBaseColor,
		fixedColor:     true, // a /color must not make a locked row look like ordinary text
		bodyTail:       vaultBodyTail,
		expand:         vaultToggle, // alt+e: unlock, or lock again
		run:            vaultRekey,  // alt+r: change the password / keyfile / token
		flashActions:   vaultFlashActions,
		// deliberately NO cliDeps: the hardware key is one optional factor of
		// three, and declaring ykchalresp a dependency would grey the whole type
		// out on every machine without a YubiKey plugged in. The prompt offers the
		// token row only when a backend is installed (crypto.TokenAvailable), and
		// a vault that WAS sealed with one names the tool it needs when it fails.
		onType:   vaultOnType,
		onRemove: func(m *Model, uuid string) { m.forgetVault(uuid) },
	})
}

// vaultSession is an open vault: the key material needed to re-seal it without
// prompting again. It is secret and session-only — never persisted, never
// synced, never logged.
type vaultSession struct {
	vault *crypto.Vault
}

// vaultSealed is the row's own account of whether it is locked. It reads the
// name because that is the one signal available to the Model-less render hooks,
// and the invariant above keeps it honest. Logic that can reach the Model
// should ask m.vaultOpen instead — that is the authoritative answer.
func vaultSealed(it *item) bool {
	return it != nil && it.typ == database.TypeEncrypted && crypto.IsGarble(it.name)
}

// vaultOpen reports whether this session holds the node's key.
func (m *Model) vaultOpen(uuid string) bool { return m.vaults[uuid] != nil }

// vaultLocked is the edit guard: typing into a locked vault's garble would
// corrupt the one thing standing in for its title. Called from editTargetOf.
func (m *Model) vaultLocked(it *item) bool {
	return it != nil && it.typ == database.TypeEncrypted && !m.vaultOpen(it.uuid)
}

// forgetVault drops a key. Every path that closes a vault goes through it, so
// "the key is gone" is one line to audit rather than four.
func (m *Model) forgetVault(uuid string) {
	if m.vaults != nil {
		delete(m.vaults, uuid)
	}
}

// ForgetAllVaults drops every session key — called on quit, so a vault opened
// in one editing session is locked again in the next.
func (m *Model) ForgetAllVaults() { m.vaults = nil }

// ── look ────────────────────────────────────────────────────────────────────

const (
	glyphVaultLocked = "▨" // hatched: the row is noise
	glyphVaultOpen   = "▧" // half-hatched: cleartext is on screen
)

func vaultGlyph(it *item) (string, string) {
	if vaultSealed(it) {
		return glyphVaultLocked, cDim
	}
	// yellow, like every other "this is live and you should notice" mark in the
	// editor: an open vault is the one state where secrets are on the screen.
	return glyphVaultOpen, cYellow
}

// vaultBaseColor keeps a locked row muted — garble is not text, and painting it
// like text invites reading it as a title somebody chose.
func vaultBaseColor(it *item) string {
	if vaultSealed(it) {
		return cDim
	}
	return ""
}

func vaultBodyTail(it *item, _ map[string]database.Chip) string {
	if vaultSealed(it) {
		return cDim + " · sealed · alt+e unlocks" + cReset
	}
	tail := cYellow + " · unlocked" + cReset
	if n := subtreeSize(it) - 1; n > 0 {
		tail += cDim + " · " + strconv.Itoa(n) + " " + plural(n, "item") + cReset
	}
	return tail
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func vaultFlashActions(m *Model, it *item) []flashAction {
	if m.vaultOpen(it.uuid) {
		return []flashAction{
			{verb: "lock", color: cGreen, do: func(m *Model, it *item) tea.Cmd { m.lockVault(it); return nil }},
			{verb: "rekey", color: cYellow, do: vaultRekey},
		}
	}
	return []flashAction{{verb: "unlock", color: cCyan, do: vaultToggle}}
}

// ── the alt+e / alt+r gestures ──────────────────────────────────────────────

// vaultToggle is alt+e: open a sealed vault (prompting for its key), or seal an
// open one. Locking is the deliberate gesture — the key is dropped, the
// cleartext subtree leaves memory, and the row goes back to noise.
func vaultToggle(m *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	if m.vaultOpen(it.uuid) {
		m.lockVault(it)
		return nil
	}
	env, err := m.vaultEnvelope(it.uuid)
	if err != nil {
		m.errorFlash("vault: " + err.Error())
		return nil
	}
	if env == nil {
		m.openVaultKey(it, vaultKeyCreate) // typed encrypted but never sealed
		return nil
	}
	m.openVaultKey(it, vaultKeyUnlock)
	return nil
}

// vaultRekey is alt+r: choose new factors for a vault that is already open. It
// requires the vault to be open, because changing the lock means re-sealing the
// contents — which you can only do if you can read them.
func vaultRekey(m *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	if !m.vaultOpen(it.uuid) {
		return vaultToggle(m, it) // unlock first; rekey is meaningless while sealed
	}
	m.openVaultKey(it, vaultKeyRekey)
	return nil
}

// vaultOnType fires right after /type makes a node encrypted: there is nothing
// to look at until factors are chosen, so go straight to the prompt.
func vaultOnType(m *Model, it *item) {
	if it == nil || vaultSealed(it) {
		return
	}
	if env, _ := m.vaultEnvelope(it.uuid); env != nil {
		// a node re-typed back to encrypted over its own old envelope: unlock it
		// rather than sealing a second vault over the first and orphaning it
		m.openVaultKey(it, vaultKeyUnlock)
		return
	}
	m.openVaultKey(it, vaultKeyCreate)
}

// ── envelope storage ────────────────────────────────────────────────────────

// vaultEnvelope reads a node's sealed envelope. A nil envelope with a nil error
// means the node has none yet — freshly typed, not yet sealed.
func (m *Model) vaultEnvelope(uuid string) (*crypto.Envelope, error) {
	if m.db == nil {
		if raw, ok := m.nodeStore(uuid)["vaultBlob"].([]byte); ok {
			return crypto.Unmarshal(raw)
		}
		return nil, nil
	}
	blob, ok, err := database.GetBlob(m.db, uuid)
	if err != nil || !ok {
		return nil, err
	}
	return crypto.Unmarshal(blob.Bytes)
}

// putVaultEnvelope writes the sealed envelope back. With no database (the
// in-memory editor the unit tests drive) it lands in the ephemeral node store,
// so the whole seal/unlock cycle is exercisable without a file on disk.
func (m *Model) putVaultEnvelope(uuid string, env *crypto.Envelope) error {
	raw, err := env.Marshal()
	if err != nil {
		return err
	}
	if m.db == nil {
		m.nodeStore(uuid)["vaultBlob"] = raw
		return nil
	}
	return database.PutBlob(m.db, database.Blob{UUID: uuid, Mime: crypto.BlobMime, Bytes: raw})
}

// ── open, seal, lock ────────────────────────────────────────────────────────

// createVault seals a node's current subtree for the first time under the given
// factors, and leaves it OPEN — you have just proven the key, so being thrown
// back out to re-enter it would be theatre.
func (m *Model) createVault(it *item, factors []crypto.Factor, s crypto.Secrets) error {
	plain, err := crypto.MarshalContent(m.vaultContentOf(it))
	if err != nil {
		return err
	}
	env, vault, err := crypto.Seal(plain, factors, s)
	if err != nil {
		return err
	}
	if err := m.putVaultEnvelope(it.uuid, env); err != nil {
		return err
	}
	m.holdVault(it, vault)
	// the children were ordinary rows a moment ago; they are cleartext now and
	// their rows have to go, or the vault would be a copy rather than a move
	m.absorbIntoVault(it)
	m.unsaved = true
	m.refreshRows()
	return nil
}

// unlockVault opens the envelope and materializes its contents as ephemeral
// children of the row.
func (m *Model) unlockVault(it *item, env *crypto.Envelope, s crypto.Secrets) error {
	plain, vault, err := env.Open(s)
	if err != nil {
		return err
	}
	content, err := crypto.UnmarshalContent(plain)
	if err != nil {
		return err
	}
	m.holdVault(it, vault)
	it.name, it.note = content.Title, content.Note
	it.children = nil
	m.materialize(it, content.Children)
	it.collapsed = false
	m.refreshRows()
	return nil
}

// rekeyVault re-seals an open vault under new factors. The content key is
// replaced too, so an attacker who had captured the old wrapped key learns
// nothing about the new envelope.
func (m *Model) rekeyVault(it *item, factors []crypto.Factor, s crypto.Secrets) error {
	plain, err := crypto.MarshalContent(m.vaultContentOf(it))
	if err != nil {
		return err
	}
	env, vault, err := crypto.Seal(plain, factors, s)
	if err != nil {
		return err
	}
	if err := m.putVaultEnvelope(it.uuid, env); err != nil {
		return err
	}
	m.holdVault(it, vault)
	m.unsaved = true
	return nil
}

func (m *Model) holdVault(it *item, v *crypto.Vault) {
	if m.vaults == nil {
		m.vaults = map[string]*vaultSession{}
	}
	m.vaults[it.uuid] = &vaultSession{vault: v}
}

// lockVault seals the subtree, drops the cleartext from memory and puts the row
// back to noise.
func (m *Model) lockVault(it *item) {
	if it == nil {
		return
	}
	garble, err := m.sealVault(it)
	if err != nil {
		m.errorFlash("vault: " + err.Error())
		return
	}
	m.dropVaultChildren(m.tree, it)
	it.name, it.note = garble, ""
	it.collapsed = false
	m.forgetVault(it.uuid)
	// a search that reached into this vault is still displaying what it found;
	// sealing the row while its contents sit two rows below is not sealing
	m.dropEncQueryResults()
	m.unsaved = true
	m.refreshRows()
	m.flash = "vault sealed"
}

// sealVault writes the node's current cleartext back into its envelope and
// returns the garble its row should wear. It does NOT touch the tree — the save
// path needs the subtree left standing while the row's name is swapped.
func (m *Model) sealVault(it *item) (string, error) {
	sess := m.vaults[it.uuid]
	if sess == nil {
		return "", crypto.ErrNoEnvelope
	}
	plain, err := crypto.MarshalContent(m.vaultContentOf(it))
	if err != nil {
		return "", err
	}
	env, err := sess.vault.Seal(plain)
	if err != nil {
		return "", err
	}
	if err := m.putVaultEnvelope(it.uuid, env); err != nil {
		return "", err
	}
	return env.Garble(), nil
}

// dropVaultChildren detaches a vault's cleartext subtree and forgets it. The
// rows were never in the database, so there is nothing to tombstone — they
// simply stop existing.
func (m *Model) dropVaultChildren(t *tree, it *item) {
	var drop func(n *item)
	drop = func(n *item) {
		for _, c := range n.children {
			drop(c)
		}
		if t != nil {
			delete(t.byUUID, n.uuid)
		}
		delete(m.nodeData, n.uuid)
	}
	for _, c := range it.children {
		drop(c)
	}
	it.children = nil
}

// lockAllVaults takes every piece of vault cleartext off the screen: each open
// vault is sealed — key dropped, subtree gone, row back to noise — and every
// Encrypted Query's results are dropped, because a result row is vault
// cleartext too and it is sitting in the outline under a perfectly ordinary
// bullet.
//
// Quit calls this BEFORE the final save, and that ordering is the point: the
// editor prints the finished outline into the terminal's scrollback on the way
// out, and scrollback is forever. A vault that was merely re-sealed in the
// database while its contents stayed on screen — or a query still showing what
// it found inside one — would be pasted, in full, into a buffer no key
// protects. This was a real leak: the search results survived the seal.
func (m *Model) lockAllVaults() {
	seen := map[*item]bool{} // the three trees may be the same tree twice
	for _, t := range []*tree{m.tree, m.tempTree, m.mainStash.tree} {
		if t == nil || t.root == nil {
			continue
		}
		var walk func(it *item)
		walk = func(it *item) {
			if seen[it] {
				return
			}
			seen[it] = true
			for _, c := range it.children {
				walk(c)
			}
			switch {
			case it.typ == database.TypeEncQuery:
				m.clearEncQuery(t, it)
			case it.typ == database.TypeEncrypted && m.vaultOpen(it.uuid):
				garble, err := m.sealVault(it)
				if err != nil {
					m.errorFlash("vault: " + err.Error())
					return
				}
				m.dropVaultChildren(t, it)
				it.name, it.note = garble, ""
				m.forgetVault(it.uuid)
			}
		}
		walk(t.root)
	}
}

// dropEncQueryResults clears every Encrypted Query's hits across the session.
// Locking ANY vault clears ALL of them: a result set can span several vaults,
// and there is no honest way to show a partial one — "these three rows are
// still readable but that one is not" is a worse answer than re-running the
// search. It is one alt+r to get them back.
func (m *Model) dropEncQueryResults() {
	seen := map[*item]bool{}
	for _, t := range []*tree{m.tree, m.tempTree, m.mainStash.tree} {
		if t == nil || t.root == nil {
			continue
		}
		var walk func(it *item)
		walk = func(it *item) {
			if seen[it] {
				return
			}
			seen[it] = true
			for _, c := range it.children {
				walk(c)
			}
			if it.typ == database.TypeEncQuery {
				m.clearEncQuery(t, it)
			}
		}
		walk(t.root)
	}
}

// clearEncQuery drops one query's decrypted rows and forgets its tally, so the
// row goes back to saying it has not run rather than claiming hits it is no
// longer showing.
func (m *Model) clearEncQuery(t *tree, it *item) {
	m.dropVaultChildren(t, it)
	delete(encQueryRuns, it.uuid)
}

// ── cleartext ⇄ items ───────────────────────────────────────────────────────

// vaultContentOf reads the live subtree back into the sealable shape.
//
// Chip anchors are EXPANDED on the way in, so what gets sealed is plain text.
// A chip is a sentinel in the name plus a row in the chips table holding the
// real value — a URL, a shell command, a linked node — and that table is not
// encrypted. Sealing the sentinel and leaving the value behind would put the
// interesting half of the secret in the clear and call it protected. The cost
// is that a chip inside a vault comes back as its text; that is the right
// trade, and GCChips sweeps the orphaned rows on the way out.
func (m *Model) vaultContentOf(it *item) crypto.Content {
	c := crypto.Content{Title: m.vaultPlain(it.name), Note: m.vaultPlain(it.note)}
	for _, ch := range it.children {
		c.Children = append(c.Children, m.vaultNodeOf(ch))
	}
	return c
}

func (m *Model) vaultNodeOf(it *item) crypto.Node {
	n := crypto.Node{
		Name: m.vaultPlain(it.name), Note: m.vaultPlain(it.note), Type: it.typ, Style: it.style,
		Collapsed: it.collapsed, Starred: it.starred, Priority: it.priority,
		CompletedAt: it.completedAt,
	}
	for _, ch := range it.children {
		n.Children = append(n.Children, m.vaultNodeOf(ch))
	}
	return n
}

func (m *Model) vaultPlain(s string) string { return database.ExpandAnchors(s, m.chips) }

// materialize turns opened cleartext into ephemeral rows beneath parent. The
// uuids are fresh every unlock: a vault child has no stable identity to leak,
// and reusing one across sessions would let a bystander correlate two blobs.
func (m *Model) materialize(parent *item, nodes []crypto.Node) {
	for _, n := range nodes {
		it, err := m.tree.newItem()
		if err != nil {
			m.errorFlash("vault: " + err.Error())
			return
		}
		it.parent = parent
		it.ephemeral = true
		it.name, it.note = n.Name, n.Note
		it.typ = n.Type
		if it.typ == "" {
			it.typ = database.TypeBullets
		}
		it.style, it.collapsed, it.starred = n.Style, n.Collapsed, n.Starred
		it.priority, it.completedAt = n.Priority, n.CompletedAt
		parent.children = append(parent.children, it)
		m.materialize(it, n.Children)
	}
}

// absorbIntoVault claims a freshly sealed node's existing children: they are
// cleartext inside the vault now, so their rows have to go. Without this,
// sealing a subtree would leave a perfectly readable copy of it in the nodes
// table beside the ciphertext.
func (m *Model) absorbIntoVault(it *item) {
	for _, c := range it.children {
		m.absorbRow(c)
	}
}

// absorbRow turns one node into vault cleartext: the row is EXPUNGED, not
// tombstoned, and expunged NOW rather than at the next save.
//
// Both halves of that matter. A tombstone sets deleted = 1 and leaves the name
// where it was, so the secret would still be sitting in the table under a flag
// that only the editor honours — `deleted` hides a row from the outline, it
// does not unwrite it. And deferring the delete to the next save would leave
// the plaintext readable for however long the session runs, next to a blob that
// already contains it. The envelope is always written before this is called, so
// there is a sealed copy by the time the row disappears.
func (m *Model) absorbRow(it *item) {
	if m.db != nil && !it.isNew {
		if err := (database.Node{UUID: it.uuid}).Expunge(m.db); err != nil {
			m.errorFlash("vault: " + err.Error())
		}
		_ = database.DeleteNodeOutput(m.db, it.uuid)
	}
	delete(m.tree.snapshots, it.uuid)
	it.isNew = true // if it ever moves back out, it is a new row again
	it.ephemeral = true
	for _, c := range it.children {
		m.absorbRow(c)
	}
}

// ── membership ──────────────────────────────────────────────────────────────

// ephemeralOwner reports a node whose whole subtree lives outside the database:
// an open vault, and an Encrypted Query (whose hits are cleartext pulled out of
// vaults — writing them down would undo the encryption one search at a time).
func (m *Model) ephemeralOwner(it *item) bool {
	switch it.typ {
	case database.TypeEncrypted:
		return m.vaultOpen(it.uuid)
	case database.TypeEncQuery:
		return true
	}
	return false
}

// reconcileVaultMembership makes "is this row cleartext?" a property of WHERE
// the row is, recomputed after every outline change, rather than a flag each
// structural operation has to remember to maintain. Indent a node into an open
// vault and it is absorbed — its row expunged, its text now only in the seal.
// Outdent one back out and it becomes an ordinary node again, inserted on the
// next save. Drag, paste, undo and /move all get this for free, because none of
// them has to know vaults exist.
//
// The vault is re-sealed BEFORE anything is expunged, so the moved-in node is
// already inside the envelope when its row goes away. Nothing exists in exactly
// one place at any point.
func (m *Model) reconcileVaultMembership() {
	if m.tree == nil || m.tree.root == nil {
		return
	}
	var absorb []*item
	owners := map[*item]bool{}
	var walk func(it *item, inside bool, owner *item)
	walk = func(it *item, inside bool, owner *item) {
		switch {
		case inside && !it.ephemeral:
			absorb = append(absorb, it)
			if owner != nil {
				owners[owner] = true
			}
			m.unsaved = true
		case !inside && it.ephemeral:
			// moved out: the user took it out of the vault, so it becomes a row
			it.ephemeral = false
			it.isNew = true
			m.unsaved = true
		}
		next := owner
		if m.ephemeralOwner(it) {
			next = it
		}
		for _, c := range it.children {
			walk(c, inside || m.ephemeralOwner(it), next)
		}
	}
	var rootOwner *item
	if m.ephemeralOwner(m.tree.root) {
		rootOwner = m.tree.root
	}
	for _, c := range m.tree.root.children {
		walk(c, rootOwner != nil, rootOwner)
	}
	if len(absorb) == 0 {
		return
	}
	for owner := range owners {
		if owner.typ == database.TypeEncrypted {
			if _, err := m.sealVault(owner); err != nil {
				m.errorFlash("vault: " + err.Error())
			}
		}
	}
	for _, it := range absorb {
		m.absorbRow(it)
	}
}

// ── the save path ───────────────────────────────────────────────────────────

// sealVaults re-seals every open vault and swaps its row's cleartext title and
// note for garble, returning the restore that puts them back. saveAll calls it
// around the write, which is what keeps the promise that no plaintext ever
// reaches the nodes table: the row the writer sees is always noise, whether or
// not the vault happens to be open on screen.
//
// It is deliberately unconditional — every open vault is re-sealed on every
// save. Sealing is a JSON marshal and one AES pass (the expensive Argon2id
// derivation happened at unlock and is not repeated), and tracking which vaults
// were dirty would be a cache whose one failure mode is writing plaintext.
func (m *Model) sealVaults() func() {
	type held struct {
		it         *item
		name, note string
	}
	var swapped []held
	// The three trees can be the same tree twice: entering the Temporary Domain
	// swaps m.tree with the stash, and after leaving it the stash may still point
	// at the main one. Sealing a vault a second time would seal the GARBLE this
	// pass just wrote into the row as the vault's title — the plaintext replaced
	// by noise inside the envelope itself.
	seen := map[*item]bool{}
	for _, t := range []*tree{m.tree, m.tempTree, m.mainStash.tree} {
		if t == nil || t.root == nil {
			continue
		}
		var walk func(it *item)
		walk = func(it *item) {
			if seen[it] {
				return
			}
			seen[it] = true
			if it.typ == database.TypeEncrypted && m.vaultOpen(it.uuid) {
				swapped = append(swapped, held{it: it, name: it.name, note: it.note})
				garble, err := m.sealVault(it)
				if err != nil {
					// Fail CLOSED. A seal that could not be written is a bad save,
					// but a save that writes the cleartext because the seal failed is
					// a disclosure — so the row still goes to noise, just noise that
					// does not match the envelope. The next successful seal recomputes
					// it, and the title is restored to the screen either way.
					m.errorFlash("vault: " + err.Error())
					garble = crypto.GarbleOf([]byte(it.uuid))
				}
				it.name, it.note = garble, ""
			}
			for _, c := range it.children {
				walk(c)
			}
		}
		walk(t.root)
	}
	return func() {
		for _, h := range swapped {
			h.it.name, h.it.note = h.name, h.note
		}
	}
}

// vaultFactorSummary describes a sealed vault's lock in one line, for the
// prompt header: what it will ask for, in the order it will ask.
func vaultFactorSummary(fs []crypto.Factor) string {
	if len(fs) == 0 {
		return "no factors"
	}
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, f.Label())
	}
	return strings.Join(parts, " + ")
}
