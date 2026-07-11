package editor

import (
	"os"
	"path/filepath"
	"strings"
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
			if th := m.thread(v.thread); th != nil {
				th.busy = false
				if th.cancel != nil {
					th.cancel()
					th.cancel = nil
				}
			}
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

// mentionVerb returns the send/stop action flash offers on a mention row.
func mentionVerb(m *Model, it *item) string {
	for _, a := range m.flashInlineRunActions(it) {
		if a.verb == "send" || a.verb == "stop" {
			return a.verb
		}
	}
	return ""
}

// TestFlashStopCancelsTurn: while a turn runs the mention offers "stop" (not
// "send"), and firing it cancels the CLI so the stream ends and the busy/cancel
// state clears.
func TestFlashStopCancelsTurn(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	defer func() { modTypes, modByKey, loadedMods = nil, map[string]nodeType{}, nil }()
	// a delay keeps the turn in-flight until we stop it
	m.tagClients["Pi"] = &tag.MockClient{Delay: 50 * time.Millisecond}

	if v := mentionVerb(m, n1); v != "send" {
		t.Fatalf("idle mention should offer send, got %q", v)
	}
	cmd := startThread(t, m, n1)
	if cmd == nil {
		t.Fatal("alt+r on a fresh mention must send")
	}
	if th := m.thread("n1"); th == nil || !th.busy || th.cancel == nil {
		t.Fatal("a running turn must record busy + a cancel func")
	}
	if v := mentionVerb(m, n1); v != "stop" {
		t.Fatalf("busy mention should offer stop, got %q", v)
	}

	m.stopThread("n1", "Pi")
	if !strings.Contains(m.flash, "stopped") {
		t.Fatalf("stop should flash a notice, got %q", m.flash)
	}

	// the canceled stream ends promptly and clears the thread state
	drain(t, m, cmd)
	if th := m.thread("n1"); th != nil && th.busy {
		t.Fatal("busy flag must clear after stop")
	}
	if th := m.thread("n1"); th != nil && th.cancel != nil {
		t.Fatal("cancel func must clear after stop")
	}
}

// TestAgentToolBandStreamsThenClears: a tool op shows the colored verb + gray
// detail (no spinner); a thinking op falls the band back to "Thinking…" rather
// than freezing on the last tool; and it all clears once the turn ends.
func TestAgentToolBandStreamsThenClears(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	defer func() { modTypes, modByKey, loadedMods = nil, map[string]nodeType{}, nil }()

	cmd := startThread(t, m, n1)
	if cmd == nil {
		t.Fatal("alt+r on a fresh mention must send")
	}
	sawTool, sawThinking := false, false
	msg := cmd()
	for i := 0; i < 60; i++ {
		ev, ok := msg.(agentEvMsg)
		if !ok {
			break // stream end
		}
		_, next := m.handleAgentEvent(ev)
		band := m.agentBandLines(row{it: n1, depth: 0}, false, 100)
		switch ev.ev.Op {
		case "tool":
			sawTool = true
			tl := m.thread("n1").tool
			if tl.name == "" {
				t.Fatal("a tool op must populate the live band")
			}
			if len(band) != 1 || !strings.Contains(band[0], displayTool(tl.name)) {
				t.Fatalf("band should show %q, got %v", displayTool(tl.name), band)
			}
		case "thinking":
			sawThinking = true
			if th := m.thread("n1"); th != nil && th.tool.name != "" {
				t.Fatal("a thinking op must drop the last tool call")
			}
			if len(band) != 1 || !strings.Contains(band[0], "Thinking") {
				t.Fatalf("band should read Thinking…, got %v", band)
			}
		}
		if next == nil {
			break
		}
		msg = next()
	}
	if !sawTool || !sawThinking {
		t.Fatalf("mock should stream tool + thinking (tool=%v thinking=%v)", sawTool, sawThinking)
	}
	if th := m.thread("n1"); th != nil && th.tool.name != "" {
		t.Fatal("the tool band must clear once the turn ends")
	}
}

func TestMentionBindsItselfAndReplyNests(t *testing.T) {
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
	// the session binds to the MENTION node itself — it is the thread root
	if th := m.thread("n1"); th == nil || !th.busy {
		t.Fatal("the mention node must own the busy flag")
	}
	drain(t, m, cmd)

	// exactly one reply, nested under the mention — never outside its subtree
	if len(n1.children) != 1 {
		t.Fatalf("want one reply under the mention, got %d children", len(n1.children))
	}
	reply := n1.children[0]
	if reply.typ != database.TypeAgent {
		t.Fatalf("reply type = %q, want agent", reply.typ)
	}
	if !hasAnchor(reply.name) {
		t.Fatalf("reply must carry chips, got plain %q", reply.name)
	}
	if len(disc.children) != 1 {
		t.Fatal("nothing may land outside the mention's subtree")
	}
	s, ok, err := database.GetThreadSession(m.db, "n1", "Pi")
	if err != nil || !ok {
		t.Fatalf("session must persist on the mention: %v", err)
	}
	if s.State != "idle" {
		t.Fatalf("session state = %q, want idle", s.State)
	}
}

func TestBoardReviewAndReplyThread(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	defer func() { modTypes, modByKey, loadedMods = nil, map[string]nodeType{}, nil }()

	// establish the session with the mention turn (alt+r) — the mention node
	// itself is the channel; its children are the board
	drain(t, m, startThread(t, m, n1))

	// a plain note on the board: seen, no reply
	note := addChild(m, n1, "note1", "retry only transient curl exits", database.TypeBullets)
	cmd, consumed := m.mentionSendOnEnter(note)
	if consumed || cmd == nil {
		t.Fatal("a note in the channel is considered without consuming Enter")
	}
	before := len(n1.children)
	drain(t, m, cmd)
	if len(n1.children) != before || len(note.children) != 0 {
		t.Fatal("a plain note must not earn a reply")
	}

	// committed CODE earns an unprompted review comment, nested ON the code
	sh := addChild(m, n1, "sh1", "./retry.sh upload 3", database.TypeBash)
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

	// a SIBLING of the mention (the old parent-bound scope) reaches no agent —
	// only the mention's own descendants are in-thread
	sib := addChild(m, disc, "sib1", "is this watched?", database.TypeBullets)
	if cmd, _ := m.mentionSendOnEnter(sib); cmd != nil {
		t.Fatal("the mention's siblings must never reach an agent")
	}
	// and a node in a different tree entirely stays silent too
	out := addChild(m, m.tree.root, "out1", "is this watched?", database.TypeBullets)
	if cmd, _ := m.mentionSendOnEnter(out); cmd != nil {
		t.Fatal("nodes outside the thread must never reach an agent")
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
	drain(t, m, startThread(t, m, n1)) // bind the session to the mention (alt+r)

	f := addChild(m, n1, "f1", "how many retries then?", database.TypeBullets)
	m.refreshRows()
	m.focusUUID, m.typedUUID = f.uuid, f.uuid
	m.cursor = m.rowIndexOf(n1) // the cursor left the typed follow-up
	if cmd := m.blurSendCheck(); cmd == nil {
		t.Fatal("leaving a typed follow-up inside a thread must send it")
	}
	if th := m.thread(f.uuid); th == nil || !th.sent {
		t.Fatal("blur send must mark the node sent")
	}

	// a second blur never resends
	m.focusUUID, m.typedUUID = f.uuid, f.uuid
	if cmd := m.blurSendCheck(); cmd != nil {
		t.Fatal("an already-sent node must not resend on blur")
	}

	// a fresh @mention keeps alt+r as the deliberate send
	g := addChild(m, n1, "g1", "@Pi a new question", database.TypeBullets)
	m.refreshRows()
	m.focusUUID, m.typedUUID = g.uuid, g.uuid
	if cmd := m.blurSendCheck(); cmd != nil {
		t.Fatal("a fresh mention must not blur-send")
	}

	// a node typed OUTSIDE the mention's subtree stays silent — the mention's
	// sibling (under the old parent-bound scope) included
	o := addChild(m, disc, "o1", "plain note", database.TypeBullets)
	m.refreshRows()
	m.focusUUID, m.typedUUID = o.uuid, o.uuid
	if cmd := m.blurSendCheck(); cmd != nil {
		t.Fatal("nodes outside the mention's subtree must not blur-send")
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

// TestAgentChipCompletion: picking an agent in the @ completer lands a real
// agent chip — red @Name token — whose expansion every mention detector reads
// as a typed "@Pi".
func TestAgentChipCompletion(t *testing.T) {
	m, _, _ := newAgentTestModel(t)
	it := addChild(m, m.tree.byUUID["disc"], "n2", "", "")
	m.refreshRows()
	m.cursor = m.findRow(it, nil)
	m.caret = 0

	m.compl = complState{kind: complAgent, start: 0}
	m.applyCompletion(it, pickerItem{value: "Pi"})

	if !hasAnchor(it.name) {
		t.Fatalf("completion left plain text, want a chip anchor: %q", it.name)
	}
	spans := anchorSpans([]rune(it.name))
	if len(spans) != 1 {
		t.Fatalf("want 1 chip anchor, got %d", len(spans))
	}
	c := m.chips[spans[0].id]
	if c.Kind != chipKindAgent || c.Value != "Pi" {
		t.Fatalf("chip = %+v, want kind=agent value=Pi", c)
	}
	if got := expandAnchors(it.name, m.chips); !strings.Contains(got, "@Pi") {
		t.Fatalf("expansion %q must read as a typed mention", got)
	}
	if ag, ok := m.mentionedAgent(expandAnchors(it.name, m.chips)); !ok || ag.Name != "Pi" {
		t.Fatal("mention detection must see the agent chip")
	}
}

// TestThreadContextParentAndChildren pins the context contract: the agent
// gets the mention's parent as ONE Parent-marked ambient line at depth 0,
// then the mention (the thread root) and its children — nothing else, and no
// node twice. Siblings of the mention never reach the agent.
func TestThreadContextParentAndChildren(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	kid := addChild(m, n1, "k1", "importer is in packages/importer", "")
	sib := addChild(m, disc, "s1", "a sibling outside the thread", "")
	m.refreshRows()

	root := m.threadRootFor(n1, tag.Agent{Name: "Pi"})
	if root != n1 {
		t.Fatalf("thread root = %q, want the mention itself", root.uuid)
	}
	thread := m.buildThread(root, n1.uuid)
	if len(thread) == 0 || !thread[0].Parent || thread[0].UUID != disc.uuid || thread[0].Depth != 0 {
		t.Fatalf("the parent must open the context as the Parent line, got %+v", thread[0])
	}
	byUUID := map[string]tag.ThreadNode{}
	for _, n := range thread {
		byUUID[n.UUID] = n
	}
	if n := byUUID["n1"]; !n.Asked || n.Depth != 1 || n.Parent {
		t.Fatalf("the mention must root the thread under the Parent line, got %+v", byUUID["n1"])
	}
	if n, ok := byUUID[kid.uuid]; !ok || n.Parent || n.Depth != 2 {
		t.Fatalf("the mention's children must be in the thread proper, got %+v ok=%v", byUUID[kid.uuid], ok)
	}
	// the mention's sibling stays outside — only the one parent line reaches
	// above the thread
	if _, ok := byUUID[sib.uuid]; ok {
		t.Fatal("the mention's sibling must not be in the context")
	}
	count := map[string]int{}
	for _, n := range thread {
		count[n.UUID]++
	}
	for uuid, c := range count {
		if c > 1 {
			t.Fatalf("node %s appears %d times in the context", uuid, c)
		}
	}
}

// TestBuildThreadNoDuplicateChildren: two mirrors of the same source inside
// the thread expand its children ONCE — the second mirror lands as a leaf, so
// the agent never reads the same subtree twice.
func TestBuildThreadNoDuplicateChildren(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	src := addChild(m, n1, "src", "shared source", "")
	addChild(m, src, "sc", "the shared child", "")
	for _, uuid := range []string{"mm1", "mm2"} {
		mir := &item{uuid: uuid, mirrorOf: "src", parent: n1}
		n1.children = append(n1.children, mir)
		m.tree.byUUID[uuid] = mir
	}
	m.refreshRows()

	count := 0
	for _, n := range m.buildThread(n1, n1.uuid) {
		if n.UUID == "sc" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the mirrored child landed %d times in the context, want once", count)
	}
}

// TestThreadRootSkipsInvisibleRoot: a top-level mention roots its own thread —
// the uuid-less tree root never becomes a session binding.
func TestThreadRootSkipsInvisibleRoot(t *testing.T) {
	m, _, _ := newAgentTestModel(t)
	m.tree.root.uuid = "" // the invisible root
	top := addChild(m, m.tree.root, "t1", "@Pi hello", "")

	if root := m.threadRootFor(top, tag.Agent{Name: "Pi"}); root != top {
		t.Fatalf("thread root = %q, want the mention itself", root.uuid)
	}
}

// TestStaleParentSessionSelfHeals: a session row bound to a node that does NOT
// mention the agent (the pre-2026-07-09 parent-binding scheme left these) must
// never make its subtree "think" — the row is deleted on sight instead.
func TestStaleParentSessionSelfHeals(t *testing.T) {
	m, disc, _ := newAgentTestModel(t)
	// simulate the legacy scheme: session bound to the NOTE (mention's parent)
	s := database.AgentSession{ID: "disc", NodeUUID: "disc", Agent: "Pi", State: "idle"}
	if err := s.Upsert(m.db); err != nil {
		t.Fatal(err)
	}

	// a new node under the note must not reach any agent…
	note := addChild(m, disc, "note1", "just a note", database.TypeBullets)
	if _, ok := m.activeThreadAgent(note); ok {
		t.Fatal("a session on a non-mention node must not cover its subtree")
	}
	// …and the stale row is gone afterwards (self-healed)
	if _, ok, _ := database.GetThreadSession(m.db, "disc", "Pi"); ok {
		t.Fatal("the stale session row must be deleted on sight")
	}
}

// TestAgentReplyEditable: agent replies are plain nodes — inline editable.
func TestAgentReplyEditable(t *testing.T) {
	if !typeOf(database.TypeAgent).inlineEditable {
		t.Fatal("agent nodes must be editable — they are just nodes")
	}
}
