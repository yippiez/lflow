package editor

import (
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

func TestMentionBindsNoteAndRepliesBelow(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	defer func() { artifactTypes, artifactByKey, loadedArtifacts = nil, map[string]nodeType{}, nil }()

	cmd, consumed := m.mentionSendOnEnter(n1)
	if !consumed || cmd == nil {
		t.Fatal("a fresh mention on Enter must send and consume the Enter")
	}
	if cmd2, c2 := m.mentionSendOnEnter(n1); c2 || cmd2 != nil {
		t.Fatal("a second Enter must not re-send")
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
	defer func() { artifactTypes, artifactByKey, loadedArtifacts = nil, map[string]nodeType{}, nil }()

	// establish the session with the mention turn
	cmd, _ := m.mentionSendOnEnter(disc.children[0])
	drain(t, m, cmd)

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

func TestMentionCreatesArtifact(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	defer func() { artifactTypes, artifactByKey, loadedArtifacts = nil, map[string]nodeType{}, nil }()
	n1.name = "@Pi create a dice artifact for me"

	cmd, sent := m.mentionSendOnEnter(n1)
	if !sent {
		t.Fatal("mention must send")
	}
	drain(t, m, cmd)

	if _, err := database.GetArtifact(m.db, "dice"); err != nil {
		t.Fatalf("dice artifact not installed: %v", err)
	}
	if typeOf("dice").label != "Dice" || typeOf("dice").run == nil {
		t.Fatal("dice artifact must hot-load into the registry, runnable")
	}
	// the install confirmation is a threaded reply on the request message
	if len(n1.children) != 1 || n1.children[0].typ != database.TypeAgent {
		t.Fatal("artifact confirmation must nest under the request message")
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
