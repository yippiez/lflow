package editor

import (
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/tag"
)

// newAgentTestModel builds a DB-backed model with the mock Pi wired in.
func newAgentTestModel(t *testing.T) (*Model, *item) {
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
	n1 := &item{uuid: "n1", name: "outline plans @Pi what do you think?", parent: root}
	root.children = append(root.children, n1)
	tr.byUUID["n1"] = n1

	m := &Model{
		db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30,
		agents:     []tag.Agent{{Name: "Pi", Mock: true}},
		tagClients: map[string]tag.Client{"Pi": &tag.MockClient{Delay: time.Nanosecond}},
	}
	m.refreshRows()
	return m, n1
}

func TestMentionSendAndReplies(t *testing.T) {
	m, n1 := newAgentTestModel(t)
	defer func() { artifactTypes, artifactByKey, loadedArtifacts = nil, map[string]nodeType{}, nil }()

	cmd, sent := m.mentionSendOnEnter(n1)
	if !sent || cmd == nil {
		t.Fatal("a fresh mention on Enter must send")
	}
	if cmd2, sent2 := m.mentionSendOnEnter(n1); sent2 || cmd2 != nil {
		t.Fatal("a second Enter must not re-send")
	}
	if !m.agentBusy["n1"] {
		t.Fatal("thread must be busy while the agent works")
	}

	// pump the stream to completion through the real update handler
	msg := cmd()
	for i := 0; i < 50; i++ {
		switch v := msg.(type) {
		case agentEvMsg:
			_, next := m.handleAgentEvent(v)
			if next == nil {
				i = 50
				break
			}
			msg = next()
		case agentStreamEndMsg:
			delete(m.agentBusy, v.thread)
			i = 50
		default:
			t.Fatalf("unexpected msg %T", msg)
		}
		if _, busy := m.agentBusy["n1"]; !busy {
			break
		}
	}

	if m.agentBusy["n1"] {
		t.Fatal("thread must be idle after done")
	}
	// exactly ONE reply node per turn — no narration/thinking nodes
	if len(n1.children) != 1 {
		t.Fatalf("want exactly 1 agent reply, got %d", len(n1.children))
	}
	reply := n1.children[0]
	if reply.typ != database.TypeAgent {
		t.Fatalf("reply type = %q, want agent", reply.typ)
	}
	// the reply's #tag and date became real chips
	if !hasAnchor(reply.name) {
		t.Fatalf("reply must carry chips, got plain %q", reply.name)
	}
	s, ok, err := database.GetThreadSession(m.db, "n1", "Pi")
	if err != nil || !ok {
		t.Fatalf("session not persisted: %v", err)
	}
	if s.State != "idle" {
		t.Fatalf("session state = %q, want idle", s.State)
	}

	// a follow-up mention on a child resolves to the same thread root
	child := n1.children[0]
	if root := m.threadRootFor(child, tag.Agent{Name: "Pi"}); root != n1 {
		t.Fatal("follow-up must continue the existing thread")
	}
}

func TestMentionCreatesArtifact(t *testing.T) {
	m, n1 := newAgentTestModel(t)
	defer func() { artifactTypes, artifactByKey, loadedArtifacts = nil, map[string]nodeType{}, nil }()
	n1.name = "@Pi create a dice artifact for me"

	cmd, sent := m.mentionSendOnEnter(n1)
	if !sent {
		t.Fatal("mention must send")
	}
	msg := cmd()
	for i := 0; i < 50; i++ {
		ev, ok := msg.(agentEvMsg)
		if !ok {
			break
		}
		_, next := m.handleAgentEvent(ev)
		if next == nil {
			break
		}
		msg = next()
	}

	if _, err := database.GetArtifact(m.db, "dice"); err != nil {
		t.Fatalf("dice artifact not installed: %v", err)
	}
	if typeOf("dice").label != "Dice" {
		t.Fatal("dice artifact must hot-load into the registry")
	}
	if typeOf("dice").run == nil {
		t.Fatal("dice artifact must be runnable")
	}
}

func TestBuildThreadGuardsMirrorCycles(t *testing.T) {
	m, n1 := newAgentTestModel(t)
	// child mirrors the thread root — a naive walk would recurse forever
	loop := &item{uuid: "m1", mirrorOf: "n1", parent: n1}
	n1.children = append(n1.children, loop)
	m.tree.byUUID["m1"] = loop

	thread := m.buildThread(n1)
	if len(thread) < 2 || len(thread) > 10 {
		t.Fatalf("mirror cycle not guarded: %d nodes", len(thread))
	}
}
