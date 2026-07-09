package editor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// newAgentTestModel builds a DB-backed model with the mock Pi wired in. The
// outline mirrors the Slack shape: root → disc (the note/channel) → n1 (the
// first board message, a mention).
func newAgentTestModel(t *testing.T) (*Model, *item, *item) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	root := &item{uuid: "root"}
	tr := &tree{
		db:            db,
		root:          root,
		byUUID:        map[string]*item{"root": root},
		externalNames: map[string]string{},
		snapshots:     map[string]snapshot{},
	}
	disc := &item{uuid: "disc", name: "importer retries", parent: root}
	root.children = append(root.children, disc)
	tr.byUUID["disc"] = disc
	n1 := &item{uuid: "n1", name: "@Pi how do i make importer retries safe?", parent: disc}
	disc.children = append(disc.children, n1)
	tr.byUUID["n1"] = n1

	m := &Model{
		db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30,
		agents:     []tag.Agent{{Name: "Pi", Mock: true}},
		tagClients: map[string]tag.Client{"Pi": &tag.MockClient{Delay: time.Nanosecond}},
	}
	m.refreshRows()
	return m, disc, n1
}

// drain runs one turn's event stream through the update handler to completion.
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	for i := 0; i < 60; i++ {
		switch v := msg.(type) {
		case agentEvMsg:
			_, next := m.handleAgentEvent(v)
			if next == nil {
				return
			}
			msg = next()
		case agentStreamEndMsg:
			delete(m.agentBusy, v.thread)
			return
		default:
			t.Fatalf("unexpected msg %T", msg)
		}
	}
	t.Fatal("event stream did not finish")
}

func addChild(m *Model, parent *item, uuid, name, typ string) *item {
	it := &item{uuid: uuid, name: name, typ: typ, parent: parent}
	parent.children = append(parent.children, it)
	m.tree.byUUID[uuid] = it
	return it
}

// startThread sends a mention node's thread the way the editor now does it:
// alt+r (Enter no longer starts sessions).
func startThread(t *testing.T, m *Model, it *item) tea.Cmd {
	t.Helper()
	ag, ok := m.mentionedAgent(expandAnchors(it.name, m.chips))
	if !ok {
		t.Fatalf("node %q mentions no configured agent", it.name)
	}
	return m.sendThread(it, ag)
}

func TestMentionBindsNoteAndRepliesBelow(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	defer func() { modTypes, modByKey, loadedMods = nil, map[string]nodeType{}, nil }()

	// Enter on a fresh mention just edits — alt+r is the deliberate send
	if cmd, consumed := m.mentionSendOnEnter(n1); consumed || cmd != nil {
		t.Fatal("Enter must not start a session; alt+r does")
	}
	cmd := startThread(t, m, n1)
	if cmd == nil {
		t.Fatal("alt+r on a fresh mention must send")
	}
	// the session binds to the mention's PARENT — the note is the channel
	if !m.agentBusy["disc"] {
		t.Fatal("the note (mention's parent) must own the busy flag")
	}
	drain(t, m, cmd)

	// exactly one reply, placed BELOW the mention as the next board message
	if len(disc.children) != 2 {
		t.Fatalf("want [question, reply] on the board, got %d children", len(disc.children))
	}
	reply := disc.children[1]
	if reply.typ != database.TypeAgent {
		t.Fatalf("board reply type = %q, want agent", reply.typ)
	}
	if !hasAnchor(reply.name) {
		t.Fatalf("reply must carry chips, got plain %q", reply.name)
	}
	if len(n1.children) != 0 {
		t.Fatal("a board answer must not nest under the question")
	}
	s, ok, err := database.GetThreadSession(m.db, "disc", "Pi")
	if err != nil || !ok {
		t.Fatalf("session must persist on the note: %v", err)
	}
	if s.State != "idle" {
		t.Fatalf("session state = %q, want idle", s.State)
	}
}

func TestBoardReviewAndReplyThread(t *testing.T) {
	m, disc, _ := newAgentTestModel(t)
	defer func() { modTypes, modByKey, loadedMods = nil, map[string]nodeType{}, nil }()

	// establish the session with the mention turn (alt+r)
	drain(t, m, startThread(t, m, disc.children[0]))

	// a plain note on the board: seen, no reply
	note := addChild(m, disc, "note1", "retry only transient curl exits", database.TypeBullets)
	cmd, consumed := m.mentionSendOnEnter(note)
	if consumed || cmd == nil {
		t.Fatal("a note in the channel is considered without consuming Enter")
	}
	before := len(disc.children)
	drain(t, m, cmd)
	if len(disc.children) != before || len(note.children) != 0 {
		t.Fatal("a plain note must not earn a reply")
	}

	// committed CODE earns an unprompted review comment, nested ON the code
	sh := addChild(m, disc, "sh1", "./retry.sh upload 3", database.TypeBash)
	cmd, _ = m.mentionSendOnEnter(sh)
	drain(t, m, cmd)
	if len(sh.children) != 1 || sh.children[0].typ != database.TypeAgent {
		t.Fatalf("want a review comment as the code node's thread, got %+v", sh.children)
	}
	review := sh.children[0]

	// continuing the conversation INSIDE the review's thread: my reply gets
	// the agent's answer below it, still inside the thread
	r1 := addChild(m, review, "r1", "good catch - cap it at how many attempts?", database.TypeBullets)
	cmd, consumed = m.mentionSendOnEnter(r1)
	if consumed || cmd == nil {
		t.Fatal("a thread reply is considered without consuming Enter")
	}
	drain(t, m, cmd)
	if len(review.children) != 2 {
		t.Fatalf("want [my reply, agent answer] in the thread, got %d", len(review.children))
	}
	answer := review.children[1]
	if answer.typ != database.TypeAgent || indexOf(answer) != indexOf(r1)+1 {
		t.Fatal("the thread answer must be my reply's next sibling")
	}

	// a node OUTSIDE the note reaches no agent
	out := addChild(m, m.tree.root, "out1", "is this watched?", database.TypeBullets)
	if cmd, _ := m.mentionSendOnEnter(out); cmd != nil {
		t.Fatal("nodes outside the session's note must never reach an agent")
	}
}

func TestMentionCreatesNodeMod(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	dir := setModTestDir(t)
	n1.name = "@Pi create a dice artifact for me"

	drain(t, m, startThread(t, m, n1))

	if _, err := os.Stat(filepath.Join(dir, "dice.js")); err != nil {
		t.Fatalf("dice.js not written to the nodes dir: %v", err)
	}
	if typeOf("dice").label != "Dice" || typeOf("dice").run == nil {
		t.Fatal("dice type must hot-load into the registry, runnable")
	}
	// the install confirmation is a threaded reply on the request message
	if len(n1.children) != 1 || n1.children[0].typ != database.TypeAgent {
		t.Fatal("install confirmation must nest under the request message")
	}
}

// TestAgentReplyChips lands a reply speaking the {{kind:value}} tokens and
// checks each becomes a real chip: anchor in the node name + a record of the
// right kind, with the cmd chip runnable-ready (its value is the command).
func TestAgentReplyChips(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	m.placeAgentNode("disc", "n1",
		"run {{cmd:go test ./...}} then read {{link:docs|https://go.dev}} and {{path:/tmp/out.log}}", "thread")

	if len(n1.children) != 1 {
		t.Fatalf("want one reply node, got %d", len(n1.children))
	}
	reply := n1.children[0]
	spans := anchorSpans([]rune(reply.name))
	if len(spans) != 3 {
		t.Fatalf("want 3 chip anchors, got %d in %q", len(spans), reply.name)
	}
	byKind := map[string]database.Chip{}
	for _, sp := range spans {
		c, ok := m.chips[sp.id]
		if !ok {
			t.Fatalf("anchor %s has no chip record", sp.id)
		}
		byKind[c.Kind] = c
	}
	if c := byKind[chipKindCmd]; c.Value != "go test ./..." {
		t.Fatalf("cmd chip value = %q", c.Value)
	}
	if c := byKind[chipKindLink]; c.Value != "https://go.dev" || c.Label != "docs" {
		t.Fatalf("link chip = %q %q", c.Label, c.Value)
	}
	if c := byKind[chipKindPath]; c.Value != "/tmp/out.log" {
		t.Fatalf("path chip value = %q", c.Value)
	}

	// a malformed or unknown token stays literal text, never a half-chip
	m.placeAgentNode("disc", "n1", "see {{frob:x}} for details", "thread")
	lit := n1.children[1]
	if hasAnchor(lit.name) || lit.name != "see {{frob:x}} for details" {
		t.Fatalf("unknown token must stay literal, got %q", lit.name)
	}
}

// TestBlurSendsTypedFollowUp: typing inside an active thread and moving the
// cursor away sends the node — alt+r stays required only for fresh @mentions.
func TestBlurSendsTypedFollowUp(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	drain(t, m, startThread(t, m, n1)) // bind the session to the note (alt+r)

	f := addChild(m, disc, "f1", "how many retries then?", database.TypeBullets)
	m.refreshRows()
	m.focusUUID, m.typedUUID = f.uuid, f.uuid
	m.cursor = m.rowIndexOf(n1) // the cursor left the typed follow-up
	if cmd := m.blurSendCheck(); cmd == nil {
		t.Fatal("leaving a typed follow-up inside a thread must send it")
	}
	if !m.mentionSent[f.uuid] {
		t.Fatal("blur send must mark the node sent")
	}

	// a second blur never resends
	m.focusUUID, m.typedUUID = f.uuid, f.uuid
	if cmd := m.blurSendCheck(); cmd != nil {
		t.Fatal("an already-sent node must not resend on blur")
	}

	// a fresh @mention keeps Enter as the deliberate send
	g := addChild(m, disc, "g1", "@Pi a new question", database.TypeBullets)
	m.refreshRows()
	m.focusUUID, m.typedUUID = g.uuid, g.uuid
	if cmd := m.blurSendCheck(); cmd != nil {
		t.Fatal("a fresh mention must not blur-send")
	}

	// a node typed OUTSIDE any active thread stays silent
	o := addChild(m, m.tree.root, "o1", "plain note", database.TypeBullets)
	m.refreshRows()
	m.focusUUID, m.typedUUID = o.uuid, o.uuid
	if cmd := m.blurSendCheck(); cmd != nil {
		t.Fatal("nodes outside a thread must not blur-send")
	}
}

func TestBuildThreadGuardsMirrorCycles(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	// child mirrors the message — a naive walk would recurse forever
	loop := &item{uuid: "m1", mirrorOf: "n1", parent: n1}
	n1.children = append(n1.children, loop)
	m.tree.byUUID["m1"] = loop

	thread := m.buildThread(n1, n1.uuid)
	if len(thread) < 2 || len(thread) > 10 {
		t.Fatalf("mirror cycle not guarded: %d nodes", len(thread))
	}
}
