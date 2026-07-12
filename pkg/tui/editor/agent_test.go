package editor

import (
	"image"
	"image/png"
	"os"
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

// TestDeleteNodeStopsRunningAgent: destroying a busy @mention (or an ancestor
// that takes it with it) must cancel the CLI immediately — same orphan rule as
// deleteRunOut for bash. A sibling delete must not touch the turn.
func TestDeleteNodeStopsRunningAgent(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	m.tagClients["Pi"] = &tag.MockClient{Delay: 50 * time.Millisecond}
	sib := addChild(m, disc, "sib", "unrelated note", "")

	cmd := startThread(t, m, n1)
	if cmd == nil {
		t.Fatal("alt+r on a fresh mention must send")
	}
	if th := m.thread("n1"); th == nil || !th.busy || th.cancel == nil {
		t.Fatal("a running turn must record busy + a cancel func")
	}

	// deleting a sibling leaves the agent alone
	m.deleteNode(sib)
	if th := m.thread("n1"); th == nil || !th.busy || th.cancel == nil {
		t.Fatal("deleting a sibling must not stop the agent")
	}

	// deleting the mention itself kills the turn right away
	m.deleteNode(n1)
	if th := m.thread("n1"); th != nil && (th.busy || th.cancel != nil) {
		t.Fatal("deleting the mention must cancel the in-flight agent")
	}
	drain(t, m, cmd) // stream close must not panic on already-cleared state
}

// TestDeleteAncestorStopsRunningAgent: removing a parent of a busy mention
// must stop the agent too (the mention rides out with the subtree).
func TestDeleteAncestorStopsRunningAgent(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	m.tagClients["Pi"] = &tag.MockClient{Delay: 50 * time.Millisecond}

	cmd := startThread(t, m, n1)
	if cmd == nil {
		t.Fatal("alt+r on a fresh mention must send")
	}
	m.deleteNode(disc)
	if th := m.thread("n1"); th != nil && (th.busy || th.cancel != nil) {
		t.Fatal("deleting an ancestor of the mention must cancel the agent")
	}
	drain(t, m, cmd)
}

// TestAgentToolBandStreamsThenClears: a tool op shows the colored verb + gray
// detail (no spinner); a thinking op falls the band back to "Thinking…" rather
// than freezing on the last tool; and it all clears once the turn ends.
func TestAgentToolBandStreamsThenClears(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)

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

	// an edit on the mention line itself never auto-thinks — alt+r is the
	// deliberate send (auto-think covers strict descendants only)
	if cmd := m.noteAgentChange(n1); cmd != nil {
		t.Fatal("editing the mention must not start a session; alt+r does")
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

	// establish the session with the mention turn (alt+r) — the mention node
	// itself is the channel; its children are the board
	drain(t, m, startThread(t, m, n1))

	// a plain note on the board: the debounced think considers it, no reply
	note := addChild(m, n1, "note1", "retry only transient curl exits", database.TypeBullets)
	if tick := m.noteAgentChange(note); tick == nil {
		t.Fatal("a note in the channel must arm the think debounce")
	}
	before := len(n1.children)
	drain(t, m, m.fireAgentThink())
	if len(n1.children) != before || len(note.children) != 0 {
		t.Fatal("a plain note must not earn a reply")
	}

	// committed CODE earns an unprompted review comment, nested ON the code
	sh := addChild(m, n1, "sh1", "./retry.sh upload 3", database.TypeBash)
	if tick := m.noteAgentChange(sh); tick == nil {
		t.Fatal("committed code must arm the think debounce")
	}
	drain(t, m, m.fireAgentThink())
	if len(sh.children) != 1 || sh.children[0].typ != database.TypeAgent {
		t.Fatalf("want a review comment as the code node's thread, got %+v", sh.children)
	}
	review := sh.children[0]

	// continuing the conversation INSIDE the review's thread: my reply gets
	// the agent's answer below it, still inside the thread — a reply node is
	// born priority down, so its sub-thread reads top-down chronological
	r1 := addChild(m, review, "r1", "good catch - cap it at how many attempts?", database.TypeBullets)
	if tick := m.noteAgentChange(r1); tick == nil {
		t.Fatal("a thread reply must arm the think debounce")
	}
	drain(t, m, m.fireAgentThink())
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
	if cmd := m.noteAgentChange(sib); cmd != nil {
		t.Fatal("the mention's siblings must never reach an agent")
	}
	// and a node in a different tree entirely stays silent too
	out := addChild(m, m.tree.root, "out1", "is this watched?", database.TypeBullets)
	if cmd := m.noteAgentChange(out); cmd != nil {
		t.Fatal("nodes outside the thread must never reach an agent")
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

// TestAgentReplyAttachments peels {{attach:…}} tokens into typed locked
// children under the agent comment — special nodes, not conversation bullets.
func TestAgentReplyAttachments(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	// comment + inline bash + block code + image caption + free-type quote
	text := "here is the fix\n" +
		"{{attach:bash|go test ./pkg/tui/editor}}\n" +
		"{{attach:code}}\n" +
		"package main\n" +
		"func main() {}\n" +
		"{{/attach}}\n" +
		"{{attach:image|architecture sketch}}\n" +
		"{{attach:quote|ship when green}}"
	m.placeAgentNode("disc", "n1", text, "thread")

	if len(n1.children) != 1 {
		t.Fatalf("want one reply, got %d", len(n1.children))
	}
	reply := n1.children[0]
	if reply.typ != database.TypeAgent || reply.name != "here is the fix" {
		t.Fatalf("reply comment = type %q name %q", reply.typ, reply.name)
	}
	if !reply.readonly {
		t.Fatal("reply must be locked")
	}
	if len(reply.children) != 4 {
		t.Fatalf("want 4 attachments, got %d", len(reply.children))
	}

	// bash → bullets + single cmd chip
	bash := reply.children[0]
	if bash.typ != database.TypeBullets || !bash.readonly {
		t.Fatalf("bash attach: type=%q readonly=%v", bash.typ, bash.readonly)
	}
	spans := anchorSpans([]rune(bash.name))
	if len(spans) != 1 {
		t.Fatalf("bash attach should be one cmd chip, got %d in %q", len(spans), bash.name)
	}
	if c := m.chips[spans[0].id]; c.Kind != chipKindCmd || c.Value != "go test ./pkg/tui/editor" {
		t.Fatalf("bash cmd chip = %+v", c)
	}

	// code block keeps multi-line body and braces
	code := reply.children[1]
	if code.typ != database.TypeCode {
		t.Fatalf("code type = %q", code.typ)
	}
	if code.name != "package main\nfunc main() {}" {
		t.Fatalf("code body = %q", code.name)
	}

	// image: caption only
	img := reply.children[2]
	if img.typ != database.TypeImage || img.name != "architecture sketch" {
		t.Fatalf("image attach = type %q name %q", img.typ, img.name)
	}

	// free-string type (quote)
	q := reply.children[3]
	if q.typ != database.TypeQuote || q.name != "ship when green" {
		t.Fatalf("quote attach = type %q name %q", q.typ, q.name)
	}
}

// TestPeelAgentAttachments covers peel order (block before inline), soft \n
// escapes, and blank-line collapse after tokens are removed.
func TestPeelAgentAttachments(t *testing.T) {
	comment, atts := peelAgentAttachments(
		"intro\n\n{{attach:code}}\nfunc f() {}\n{{/attach}}\n\n" +
			"mid {{attach:bash|echo hi}} end\n" +
			"{{attach:quote|line one\\nline two}}",
	)
	if comment != "intro\n\nmid  end" {
		t.Fatalf("comment = %q", comment)
	}
	if len(atts) != 3 {
		t.Fatalf("atts = %+v", atts)
	}
	if atts[0].typ != "code" || atts[0].body != "func f() {}" {
		t.Fatalf("code = %+v", atts[0])
	}
	if atts[1].typ != "bash" || atts[1].body != "echo hi" {
		t.Fatalf("bash = %+v", atts[1])
	}
	if atts[2].typ != "quote" || atts[2].body != "line one\nline two" {
		t.Fatalf("quote = %+v", atts[2])
	}

	// agent type is refused at place time; peel still surfaces it
	_, atts = peelAgentAttachments("{{attach:agent|nope}}")
	if len(atts) != 1 || atts[0].typ != "agent" {
		t.Fatalf("peel should still surface agent type, got %+v", atts)
	}
}

// TestAgentAttachmentImageFromFile loads pixels when the attach body is a path
// to an existing image (optional |caption).
func TestAgentAttachmentImageFromFile(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	dir := t.TempDir()
	path := dir + "/dot.png"
	// 1×1 PNG via the same encoder loadImageAttach uses
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img1 := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if err := png.Encode(f, img1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	m.placeAgentNode("disc", "n1",
		"see diagram {{attach:image|"+path+"|dot}}", "thread")
	reply := n1.children[0]
	if len(reply.children) != 1 {
		t.Fatalf("want one image attach, got %d", len(reply.children))
	}
	im := reply.children[0]
	if im.typ != database.TypeImage || im.name != "dot" {
		t.Fatalf("image = type %q name %q", im.typ, im.name)
	}
	blob, ok, err := database.GetBlob(m.db, im.uuid)
	if err != nil || !ok || len(blob.Bytes) == 0 {
		t.Fatalf("blob missing: ok=%v err=%v len=%d", ok, err, len(blob.Bytes))
	}
}

// TestDebouncedThinkOnFollowUp: typing inside an active thread arms the
// debounced think; the settled tick sends, a newer edit supersedes an armed
// one, and nodes outside the thread never arm.
func TestDebouncedThinkOnFollowUp(t *testing.T) {
	m, disc, n1 := newAgentTestModel(t)
	drain(t, m, startThread(t, m, n1)) // bind the session to the mention (alt+r)

	f := addChild(m, n1, "f1", "how many retries then?", database.TypeBullets)
	m.refreshRows()
	m.markAgentTouch(f)
	if m.agentTouched != f.uuid {
		t.Fatal("an edit must record the touched node for Update to drain")
	}
	m.agentTouched = ""
	if tick := m.noteAgentChange(f); tick == nil {
		t.Fatal("a typed follow-up inside a thread must arm the think debounce")
	}
	gen := m.agentThinkGen

	// a newer edit supersedes the armed tick — only the latest gen fires
	if tick := m.noteAgentChange(f); tick == nil || m.agentThinkGen != gen+1 {
		t.Fatal("a newer edit must re-arm with a fresh generation")
	}

	// the settled debounce sends the thread
	if cmd := m.fireAgentThink(); cmd == nil {
		t.Fatal("the settled debounce must send the follow-up")
	}
	// and the change slot is consumed — a stray fire sends nothing
	if cmd := m.fireAgentThink(); cmd != nil {
		t.Fatal("a consumed change must not resend")
	}

	// a node typed OUTSIDE the mention's subtree stays silent — the mention's
	// sibling (under the old parent-bound scope) included
	o := addChild(m, disc, "o1", "plain note", database.TypeBullets)
	m.refreshRows()
	if cmd := m.noteAgentChange(o); cmd != nil {
		t.Fatal("nodes outside the mention's subtree must not arm the debounce")
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
// TestBuildThreadTypedXML: buildThread folds each type's toContext hook into
// the wire nodes, so a todo carries its checkbox state, a log its timestamp,
// a json node its multi-line document, and a plain bullet nothing extra.
func TestBuildThreadTypedXML(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	todo := addChild(m, n1, "t1", "ship the release", database.TypeTodo)
	todo.completedAt = 1
	lg := addChild(m, n1, "l1", "cut at 14:00", database.TypeLog)
	lg.addedOn = time.Date(2026, 7, 11, 14, 0, 0, 0, time.Local).UnixNano()
	addChild(m, n1, "j1", "{\n  \"env\": \"prod\"\n}", database.TypeJSON)
	addChild(m, n1, "b1", "plain bullet", database.TypeBullets)

	got := map[string]tag.ThreadNode{}
	for _, n := range m.buildThread(n1, n1.uuid) {
		got[n.UUID] = n
	}
	if n := got["t1"]; n.XMLTag != "todo" || n.XMLAttrs != `done="true"` {
		t.Fatalf("todo xml = %q %q, want todo done=\"true\"", n.XMLTag, n.XMLAttrs)
	}
	if n := got["l1"]; n.XMLTag != "log" || n.XMLAttrs != `time="2026-07-11 14:00"` {
		t.Fatalf("log xml = %q %q", n.XMLTag, n.XMLAttrs)
	}
	if n := got["j1"]; n.XMLTag != "json" || n.XMLBody != "{\n  \"env\": \"prod\"\n}" {
		t.Fatalf("json xml = %q body %q", n.XMLTag, n.XMLBody)
	}
	if n := got["b1"]; n.XMLTag != "" || n.XMLAttrs != "" || n.XMLBody != "" {
		t.Fatalf("a bullet must carry no type xml, got %q %q %q", n.XMLTag, n.XMLAttrs, n.XMLBody)
	}
}

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
