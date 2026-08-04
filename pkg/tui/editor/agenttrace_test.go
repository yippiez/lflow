package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// a session that asks, answers, edits a file and writes a new one — every shape
// the transcript has to turn into a node
const (
	recAgentSay = `{"type":"assistant","message":{"role":"assistant","content":[` +
		`{"type":"text","text":"looking at it now"}]}}`
	recAgentEdit = `{"type":"assistant","message":{"role":"assistant","content":[` +
		`{"type":"tool_use","name":"Edit","input":{"file_path":"sync_test.go",` +
		`"old_string":"func TestSync(t *testing.T) {","new_string":"func TestSync(t *testing.T) {\n\tt.Parallel()"}}]}}`
	recAgentWrite = `{"type":"assistant","message":{"role":"assistant","content":[` +
		`{"type":"tool_use","name":"Write","input":{"file_path":"NOTES.md","content":"first line\nsecond line"}}]}}`
	recAgentRead = `{"type":"assistant","message":{"role":"assistant","content":[` +
		`{"type":"tool_use","name":"Read","input":{"file_path":"sync_test.go"}}]}}`
)

// agentNodeOn builds a db-backed model holding one Agent node bound to a seeded
// Claude Code session.
func agentNodeOn(t *testing.T, records ...string) (*Model, *item) {
	t.Helper()
	id := claudeStore(t, records...)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "a", Name: "", Type: database.TypeAgent})
	it := m.tree.byUUID["a"]
	m.bindAgentNode("a", c, agentStoreSession{variant: c.id, id: id, title: "fix the flaky sync test"})
	m.focused = false
	m.refreshRows()
	return m, it
}

// TestAgentNodeTranscribesItsSession: the children ARE the transcript — real
// nodes, one per turn, so they fold, link, search and query like anything else.
func TestAgentNodeTranscribesItsSession(t *testing.T) {
	m, it := agentNodeOn(t, recSummary, recUser, recAgentSay, recAgentEdit)
	m.agentTranscribe(it)

	if len(it.children) < 3 {
		t.Fatalf("got %d turns:\n%s", len(it.children), turnDump(it))
	}
	dump := turnDump(it)
	for _, want := range []string{"you · the sync test is flaky", "agent · looking at it now"} {
		if !strings.Contains(dump, want) {
			t.Errorf("transcript is missing %q:\n%s", want, dump)
		}
	}
	// every turn is a REAL node, and every one of them is locked
	for _, c := range it.children {
		if c.uuid == "" {
			t.Error("a turn is not a real node — it has no uuid")
		}
		if !c.readonly || !c.structureLocked {
			t.Errorf("turn %q is editable (readonly=%v structure=%v)", c.name, c.readonly, c.structureLocked)
		}
		if m.editTargetOf(c) != nil {
			t.Errorf("turn %q offered an edit target", c.name)
		}
	}
}

// TestAgentNodeWriteToolBecomesADiff: a write tool recorded what it wrote, so
// that turn is a Diff node rather than a line of JSON.
func TestAgentNodeWriteToolBecomesADiff(t *testing.T) {
	m, it := agentNodeOn(t, recSummary, recUser, recAgentEdit, recAgentWrite, recAgentRead)
	m.agentTranscribe(it)

	var diffs []*item
	for _, c := range it.children {
		if c.typ == database.TypeDiff {
			diffs = append(diffs, c)
		}
	}
	if len(diffs) != 2 {
		t.Fatalf("got %d diff nodes, want one per write tool:\n%s", len(diffs), turnDump(it))
	}
	edit := diffs[0]
	if diffPath(edit.name) != "sync_test.go" {
		t.Errorf("diff path = %q", diffPath(edit.name))
	}
	if !strings.Contains(edit.name, "-func TestSync(t *testing.T) {") {
		t.Errorf("the removed side is missing:\n%s", edit.name)
	}
	if !strings.Contains(edit.name, "+\tt.Parallel()") {
		t.Errorf("the added side is missing:\n%s", edit.name)
	}
	// a whole-file write has no removed side
	write := diffs[1]
	if a, r := diffStat(write.name); a != 2 || r != 0 {
		t.Errorf("write diff stat = +%d −%d, want +2 −0:\n%s", a, r, write.name)
	}
	// a READ tool is not a write, so it stays an ordinary turn
	if strings.Count(turnDump(it), "type=diff") != 2 {
		t.Errorf("a non-write tool became a diff:\n%s", turnDump(it))
	}
}

// TestAgentNodeRetranscribeKeepsTurnIdentity: a session that has moved on gains
// its new turns and keeps the uuids of the ones already there, so a link into
// the middle of a transcript survives the refresh.
func TestAgentNodeRetranscribeKeepsTurnIdentity(t *testing.T) {
	m, it := agentNodeOn(t, recSummary, recUser, recAgentSay)
	m.agentTranscribe(it)
	if len(it.children) < 2 {
		t.Fatalf("first pass produced %d turns", len(it.children))
	}
	first := it.children[0].uuid

	// the same session, one turn longer
	m.nodeStore("a")["agentTrace"] = append(m.agentTraces(mustHandle(t, m, it)),
		agentTrace{kind: "assistant", text: "and it is fixed"})
	m.agentTranscribe(it)

	if it.children[0].uuid != first {
		t.Errorf("the first turn was rebuilt: %q → %q", first, it.children[0].uuid)
	}
}

// TestAgentNodeWearsTheSessionPill: the node and the chip are two placements of
// one session, so the row reads the same filled pill the chip does.
func TestAgentNodeWearsTheSessionPill(t *testing.T) {
	m, it := agentNodeOn(t, recSummary, recUser)
	got := m.agentNodeRender(it)
	if !strings.Contains(stripSGR(got), "✽ fix the flaky sync test") {
		t.Errorf("the row is not the session's pill: %q", stripSGR(got))
	}
	if !strings.Contains(got, bgOf(agentColorSGR("#4ec9b0"))) {
		t.Errorf("the pill is not filled with the session's color: %q", got)
	}
	if !strings.Contains(stripSGR(got), "transcribe") {
		t.Errorf("an untranscribed session does not say how to read it: %q", stripSGR(got))
	}
	m.agentTranscribe(it)
	if got := stripSGR(m.agentNodeRender(it)); !strings.Contains(got, "turn") {
		t.Errorf("a transcribed session does not count its turns: %q", got)
	}
}

// TestAgentNodeExpandTranscribes: expanding a session is asking what happened in
// it, so an empty fold answers by writing the transcript.
func TestAgentNodeExpandTranscribes(t *testing.T) {
	m, it := agentNodeOn(t, recSummary, recUser, recAgentSay)
	m.cursor = m.rowIndexOf(it)
	if len(it.children) != 0 {
		t.Fatal("the fixture starts with turns already")
	}
	m.expandStep()
	if lockedTurnCount(it) == 0 {
		t.Error("expanding an Agent node did not transcribe it")
	}
}

// TestDiffNodeIsReadOnly: a diff is a record of something that already happened.
func TestDiffNodeIsReadOnly(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "d",
		Name: "--- a.go\n-old line\n+new line", Type: database.TypeDiff})
	it := m.tree.byUUID["d"]
	it.readonly = true
	it.structureLocked = true
	m.refreshRows()
	m.cursor = m.rowIndexOf(it)

	if m.editTarget() != nil {
		t.Error("a diff offered an edit target")
	}
	before := it.name
	m.press("x")
	m.press("backspace")
	if it.name != before {
		t.Errorf("a diff took an edit: %q", it.name)
	}
	// the row says which file and how big the change is
	row := stripSGR(diffRender(it, it.name))
	for _, want := range []string{"a.go", "+1", "−1"} {
		if !strings.Contains(row, want) {
			t.Errorf("diff row %q is missing %q", row, want)
		}
	}
	// and ⌥e reads it, colored by margin
	band := strings.Join(diffViewContent(it, "", 80), "\n")
	if !strings.Contains(band, cGreen+"+new line") {
		t.Errorf("added line is not green:\n%q", band)
	}
	if !strings.Contains(band, cRed+"-old line") {
		t.Errorf("removed line is not red:\n%q", band)
	}
}

// TestDiffNeverSynthesized: a tool that did not record what it wrote yields no
// diff at all, rather than one lflow made up by reading the file.
func TestDiffNeverSynthesized(t *testing.T) {
	if d := agentToolDiff("Edit", map[string]any{"file_path": "a.go"}); d != nil {
		t.Errorf("a write with no recorded text produced a diff: %+v", d)
	}
	if d := agentToolDiff("Bash", map[string]any{"command": "rm -rf /"}); d != nil {
		t.Error("a non-write tool produced a diff")
	}
	if d := agentToolDiff("MultiEdit", map[string]any{"file_path": "a.go", "edits": []any{
		map[string]any{"old_string": "a", "new_string": "b"},
		map[string]any{"old_string": "c", "new_string": "d"},
	}}); d == nil || len(d.hunks) != 2 {
		t.Errorf("multi-edit diff = %+v", d)
	}
}

func mustHandle(t *testing.T, m *Model, it *item) agentHandle {
	t.Helper()
	h, ok := agentNodeHandle(m, it)
	if !ok {
		t.Fatal("no agent handle on the node")
	}
	return h
}

func turnDump(it *item) string {
	var b strings.Builder
	for _, c := range it.children {
		b.WriteString("type=" + c.typ + " " + strings.ReplaceAll(c.name, "\n", "⏎") + "\n")
	}
	return b.String()
}
