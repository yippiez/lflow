package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/crypto"
	"github.com/lflow/lflow/tui/database"
)

// newVaultModel builds a DB-backed editor holding one node destined to become a
// vault, with the subtree given as its children.
func newVaultModel(t *testing.T, title string, children ...string) (*Model, *item) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	insert := func(n database.Node) {
		t.Helper()
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	insert(database.Node{UUID: "root", Name: "root", Type: database.TypeBullets, AddedOn: 1, EditedOn: 1})
	insert(database.Node{UUID: "vault", ParentUUID: "root", Name: title, Type: database.TypeBullets, AddedOn: 2, EditedOn: 2})

	root := &item{uuid: "root", name: "root"}
	vault := &item{uuid: "vault", name: title, typ: database.TypeBullets, parent: root}
	root.children = []*item{vault}
	byUUID := map[string]*item{"root": root, "vault": vault}
	snaps := map[string]snapshot{
		"root":  {name: "root", typ: database.TypeBullets},
		"vault": {parentUUID: "root", name: title, typ: database.TypeBullets},
	}
	for i, name := range children {
		uuid := "kid" + string(rune('a'+i))
		insert(database.Node{UUID: uuid, ParentUUID: "vault", Name: name, Rank: i,
			Type: database.TypeBullets, AddedOn: int64(10 + i), EditedOn: int64(10 + i)})
		kid := &item{uuid: uuid, name: name, typ: database.TypeBullets, parent: vault}
		vault.children = append(vault.children, kid)
		byUUID[uuid] = kid
		snaps[uuid] = snapshot{parentUUID: "vault", rank: i, name: name, typ: database.TypeBullets}
	}
	tr := &tree{db: db, root: root, byUUID: byUUID, snapshots: snaps, externalNames: map[string]string{}}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30,
		chips: map[string]database.Chip{}}
	m.ctx.DB = db
	m.refreshRows()
	return m, vault
}

// seal converts the node and answers the create prompt with a password.
func seal(t *testing.T, m *Model, it *item, password string) {
	t.Helper()
	m.setNodeType(it, database.TypeEncrypted)
	vaultOnType(m, it)
	if m.mode != modeVaultKey {
		t.Fatalf("typing a node encrypted did not open the key prompt (mode %v)", m.mode)
	}
	m.vaultKey.pass = textField{value: password}
	m.vaultKey.confirm = textField{value: password}
	m.submitVaultKey()
	if m.mode == modeVaultKey {
		t.Fatalf("the create prompt refused: %s", m.vaultKey.err)
	}
}

func TestSealingHidesTheSubtreeBehindGarble(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00", "pin 4417")
	seal(t, m, vault, "hunter2")

	// the row is open right after sealing — you just proved the key
	if !m.vaultOpen("vault") {
		t.Fatal("the vault was not left open after being created")
	}
	if len(vault.children) != 2 {
		t.Fatalf("the subtree vanished on seal: %d children", len(vault.children))
	}
	for _, c := range vault.children {
		if !c.ephemeral {
			t.Errorf("child %q is still a database row inside a vault", c.name)
		}
	}

	// locking puts the row back to noise and takes the cleartext out of memory
	m.lockVault(vault)
	if m.vaultOpen("vault") {
		t.Error("the key survived the lock")
	}
	if len(vault.children) != 0 {
		t.Errorf("the cleartext subtree survived the lock: %d children", len(vault.children))
	}
	if !crypto.IsGarble(vault.name) {
		t.Errorf("a locked vault's name is %q, not garble", vault.name)
	}
	if strings.Contains(vault.name, "bank") {
		t.Error("the locked row still says what it is")
	}

	// …and unlocking brings it all back
	env, err := m.vaultEnvelope("vault")
	if err != nil || env == nil {
		t.Fatalf("envelope: %v", err)
	}
	if err := m.unlockVault(vault, env, crypto.Secrets{Password: []byte("hunter2")}); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if vault.name != "bank" {
		t.Errorf("title after unlock = %q, want %q", vault.name, "bank")
	}
	var names []string
	for _, c := range vault.children {
		names = append(names, c.name)
	}
	if strings.Join(names, ",") != "iban TR00,pin 4417" {
		t.Errorf("subtree after unlock = %v", names)
	}
}

func TestSealedContentsNeverReachTheNodesTable(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00", "pin 4417")
	seal(t, m, vault, "hunter2")

	// edit inside the open vault, then save with it still open — the save path
	// must show the writer garble, not the cleartext on screen
	vault.children[0].name = "iban TR99 SECRET"
	m.unsaved = true
	if _, err := m.saveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows, err := m.db.Query("SELECT uuid, name FROM nodes WHERE deleted = 0")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := map[string]string{}
	for rows.Next() {
		var uuid, name string
		if err := rows.Scan(&uuid, &name); err != nil {
			t.Fatal(err)
		}
		found[uuid] = name
	}
	for uuid, name := range found {
		for _, secret := range []string{"bank", "iban", "pin", "SECRET", "TR99"} {
			if strings.Contains(name, secret) {
				t.Errorf("node %s leaks %q into the nodes table: %q", uuid, secret, name)
			}
		}
	}
	if !crypto.IsGarble(found["vault"]) {
		t.Errorf("the vault row holds %q, not garble", found["vault"])
	}
	if _, ok := found["kida"]; ok {
		t.Error("a vault child kept its own row")
	}

	// the cleartext is back on screen after the save, unchanged
	if vault.name != "bank" || vault.children[0].name != "iban TR99 SECRET" {
		t.Errorf("the save did not restore the open vault: %q / %q", vault.name, vault.children[0].name)
	}

	// and the edit really did land in the envelope
	m.lockVault(vault)
	env, _ := m.vaultEnvelope("vault")
	if env == nil {
		t.Fatal("no envelope after save")
	}
	plain, _, err := env.Open(crypto.Secrets{Password: []byte("hunter2")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !strings.Contains(string(plain), "TR99 SECRET") {
		t.Error("the edit made while open was not sealed")
	}
}

// A Temporary Domain round trip can leave m.mainStash.tree pointing at the same
// tree as m.tree. sealVaults walks both, and a second pass over an already
// swapped row would seal the GARBLE as the vault's title — replacing the
// plaintext with noise inside the envelope, permanently.
func TestSaveDoesNotSealTheGarbleWhenATreeIsWalkedTwice(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00")
	seal(t, m, vault, "hunter2")
	m.mainStash.tree = m.tree

	m.unsaved = true
	if _, err := m.saveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if vault.name != "bank" {
		t.Errorf("the open vault's title after save = %q, want %q", vault.name, "bank")
	}

	m.lockVault(vault)
	env, _ := m.vaultEnvelope("vault")
	if env == nil {
		t.Fatal("no envelope")
	}
	plain, _, err := env.Open(crypto.Secrets{Password: []byte("hunter2")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	content, err := crypto.UnmarshalContent(plain)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	if content.Title != "bank" {
		t.Errorf("the sealed title is %q — the second pass sealed the garble", content.Title)
	}
	if len(content.Children) != 1 || content.Children[0].Name != "iban TR00" {
		t.Errorf("the sealed subtree is %+v", content.Children)
	}
}

func TestWrongPasswordKeepsThePromptAndSaysNothing(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00")
	seal(t, m, vault, "hunter2")
	m.lockVault(vault)

	vaultToggle(m, vault)
	if m.mode != modeVaultKey {
		t.Fatalf("alt+e on a sealed vault did not prompt (mode %v)", m.mode)
	}
	m.vaultKey.pass = textField{value: "not it"}
	m.submitVaultKey()
	if m.mode != modeVaultKey {
		t.Fatal("a wrong password closed the prompt")
	}
	if m.vaultKey.err == "" {
		t.Error("a wrong password reported nothing")
	}
	if m.vaultKey.pass.value != "" {
		t.Error("the rejected password was left in the field")
	}
	if m.vaultOpen("vault") || len(vault.children) != 0 {
		t.Error("a wrong password opened the vault")
	}

	m.vaultKey.pass = textField{value: "hunter2"}
	m.submitVaultKey()
	if m.mode == modeVaultKey {
		t.Fatalf("the right password was refused: %s", m.vaultKey.err)
	}
	if !m.vaultOpen("vault") {
		t.Error("the right password did not open the vault")
	}
}

func TestLockedVaultTextIsNotEditable(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00")
	seal(t, m, vault, "hunter2")
	if m.editTargetOf(vault) == nil {
		t.Error("an OPEN vault's title should be editable — it is just a bullet")
	}
	m.lockVault(vault)
	if m.editTargetOf(vault) != nil {
		t.Error("a sealed vault's garble is editable; typing would destroy the row")
	}
}

func TestSealedVaultCannotBeRetyped(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00")
	seal(t, m, vault, "hunter2")
	m.lockVault(vault)

	m.setNodeType(vault, database.TypeBullets)
	if vault.typ != database.TypeEncrypted {
		t.Error("a sealed vault was retyped, stranding its envelope")
	}
	if !m.flashErr {
		t.Error("the refusal was silent")
	}
}

func TestOrdinaryQueryCannotSeeIntoAnOpenVault(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00")
	seal(t, m, vault, "hunter2")

	q, err := m.tree.newItem()
	if err != nil {
		t.Fatal(err)
	}
	q.typ = database.TypeQuery
	q.name = "iban"
	q.parent = m.tree.root
	m.tree.root.children = append(m.tree.root.children, q)

	for _, hit := range m.queryMatches(q) {
		if strings.Contains(hit.Name, "iban") {
			t.Fatalf("the ordinary query reached inside a vault: %q", hit.Name)
		}
	}
}

func TestEncryptedQueryFindsWhatTheOrdinaryOneCannot(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00", "pin 4417")
	seal(t, m, vault, "hunter2")

	q, err := m.tree.newItem()
	if err != nil {
		t.Fatal(err)
	}
	q.typ = database.TypeEncQuery
	q.name = "iban"
	q.parent = m.tree.root
	m.tree.root.children = append(m.tree.root.children, q)

	// the vault is open, so no key prompt is needed
	if cmd := runEncQuery(m, q); m.mode == modeVaultKey {
		_ = cmd
		t.Fatal("a search with every vault already open still prompted")
	}
	if len(q.children) != 1 || q.children[0].name != "iban TR00" {
		var got []string
		for _, c := range q.children {
			got = append(got, c.name)
		}
		t.Fatalf("encrypted query hits = %v, want [iban TR00]", got)
	}
	if !q.children[0].ephemeral {
		t.Error("a decrypted hit was materialized as a persistable row")
	}
	if d, _ := q.encQueryStats(); d.hits != 1 || d.opened != 1 || d.sealed != 0 {
		t.Errorf("tally = %+v, want 1 hit in 1 vault, 0 sealed", d)
	}
}

func TestEncryptedQueryPromptsForSealedVaultsAndCountsRefusals(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00")
	seal(t, m, vault, "hunter2")
	m.lockVault(vault)

	q, err := m.tree.newItem()
	if err != nil {
		t.Fatal(err)
	}
	q.typ = database.TypeEncQuery
	q.name = "iban"
	q.parent = m.tree.root
	m.tree.root.children = append(m.tree.root.children, q)

	runEncQuery(m, q)
	if m.mode != modeVaultKey {
		t.Fatal("a search with a sealed vault did not ask for a key")
	}

	// a key that opens nothing: the vault is counted, never named
	m.vaultKey.pass = textField{value: "wrong"}
	m.submitVaultKey()
	if d, _ := q.encQueryStats(); d.hits != 0 || d.sealed != 1 {
		t.Errorf("after a bad key: %+v, want 0 hits and 1 sealed", d)
	}
	if len(q.children) != 0 {
		t.Error("a failed search still materialized rows")
	}

	// the right one opens it without unlocking the row in the outline
	runEncQuery(m, q)
	m.vaultKey.pass = textField{value: "hunter2"}
	m.submitVaultKey()
	if len(q.children) != 1 || q.children[0].name != "iban TR00" {
		t.Fatalf("the search did not find the sealed vault's contents: %d hits", len(q.children))
	}
	if m.vaultOpen("vault") {
		t.Error("searching a vault silently left it unlocked in the outline")
	}
	if !crypto.IsGarble(vault.name) {
		t.Error("searching a vault revealed its title in the outline")
	}
}

func TestQuittingSealsAnOpenVaultBeforeTheScrollbackDump(t *testing.T) {
	m, vault := newVaultModel(t, "bank", "iban TR00")
	seal(t, m, vault, "hunter2")

	m.quit()
	if m.vaultOpen("vault") {
		t.Error("quit left a key in memory")
	}
	if !crypto.IsGarble(vault.name) {
		t.Errorf("quit left the cleartext title on the row: %q", vault.name)
	}
	dump := strings.Join(m.finalView(100), "\n")
	for _, secret := range []string{"bank", "iban", "TR00"} {
		if strings.Contains(dump, secret) {
			t.Errorf("the exit render leaks %q into the terminal scrollback", secret)
		}
	}
}

func TestMovingANodeIntoAnOpenVaultTakesItsRowAway(t *testing.T) {
	m, vault := newVaultModel(t, "bank")
	seal(t, m, vault, "hunter2")

	// an ordinary node elsewhere in the outline
	outside := &item{uuid: "outside", name: "plain text", typ: database.TypeBullets, parent: m.tree.root}
	m.tree.root.children = append(m.tree.root.children, outside)
	m.tree.byUUID["outside"] = outside
	m.tree.snapshots["outside"] = snapshot{parentUUID: "root", name: "plain text", typ: database.TypeBullets}

	// …indented into the vault
	m.tree.root.children = m.tree.root.children[:len(m.tree.root.children)-1]
	outside.parent = vault
	vault.children = append(vault.children, outside)
	m.refreshRows()

	if !outside.ephemeral {
		t.Fatal("a node moved into a vault kept its database row")
	}
	// expunged, not tombstoned: a `deleted = 1` row keeps its name, so the
	// secret would still be sitting in the table behind a flag only the editor
	// honours. And gone NOW, not at the next save.
	var name string
	err := m.db.QueryRow("SELECT name FROM nodes WHERE uuid = 'outside'").Scan(&name)
	if err == nil {
		t.Errorf("the moved node's row is still readable in the table: %q", name)
	}

	// …and back out again: an ordinary node once more
	vault.children = nil
	outside.parent = m.tree.root
	m.tree.root.children = append(m.tree.root.children, outside)
	m.refreshRows()
	if outside.ephemeral {
		t.Error("a node taken out of a vault stayed unpersistable — it would be lost on save")
	}
	if !outside.isNew {
		t.Error("a node taken out of a vault must be inserted as a fresh row")
	}
}

func TestVaultKeyPromptMasksThePasswordAndNamesTheSuite(t *testing.T) {
	m, vault := newVaultModel(t, "bank")
	m.setNodeType(vault, database.TypeEncrypted)
	vaultOnType(m, vault)
	for _, r := range "hunter2" {
		m.press(string(r))
	}
	page := strings.Join(m.viewVaultKey(100), "\n")
	if strings.Contains(page, "hunter2") {
		t.Error("the prompt echoes the password")
	}
	if !strings.Contains(page, "***") {
		t.Error("the prompt shows no mask for what was typed")
	}
	if !strings.Contains(page, crypto.Suite) {
		t.Error("the prompt does not say what it is about to encrypt with")
	}
	// alt+p reveals it for the length of one look
	m.press("alt+p")
	if !strings.Contains(strings.Join(m.viewVaultKey(100), "\n"), "hunter2") {
		t.Error("alt+p did not reveal the password")
	}
}

func TestVaultNeedsAtLeastOneFactor(t *testing.T) {
	m, vault := newVaultModel(t, "bank")
	m.setNodeType(vault, database.TypeEncrypted)
	vaultOnType(m, vault)
	m.submitVaultKey() // nothing typed
	if m.mode != modeVaultKey {
		t.Fatal("an empty prompt sealed the node with no key at all")
	}
	if !strings.Contains(m.vaultKey.err, "at least one") {
		t.Errorf("error = %q", m.vaultKey.err)
	}

	m.vaultKey.pass = textField{value: "one"}
	m.vaultKey.confirm = textField{value: "two"}
	m.submitVaultKey()
	if m.mode != modeVaultKey || !strings.Contains(m.vaultKey.err, "do not match") {
		t.Errorf("a mistyped confirmation was accepted: %q", m.vaultKey.err)
	}
}
