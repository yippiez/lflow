package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/tui/database"
)

// bnode builds a bash item tree for the composition tests.
func bnode(name string, kids ...*item) *item {
	it := &item{name: name, typ: database.TypeBash, uuid: "u-" + name}
	for _, k := range kids {
		k.parent = it
		it.children = append(it.children, k)
	}
	return it
}

func plain(s string) string { return s }

// TestBashComposeShapes pins the four composition rules: a head prefixes its
// children, a join operator joins them, a wrapper wraps them, and empty text is a
// plain container.
func TestBashComposeShapes(t *testing.T) {
	cases := []struct {
		name string
		it   *item
		want string
	}{
		{"leaf", bnode("echo hi"), "echo hi"},
		{"head", bnode("rg", bnode("-n"), bnode("TODO"), bnode("tui/editor")), "rg -n TODO tui/editor"},
		{"pipe", bnode("|", bnode("ls -la"), bnode("wc -l")), "ls -la | wc -l"},
		{"and", bnode("&&", bnode("go build ./..."), bnode("go test ./...")), "go build ./... && go test ./..."},
		{"semi", bnode(";", bnode("date"), bnode("uptime")), "date ; uptime"},
		{"container", bnode("", bnode("git"), bnode("status")), "git status"},
		{"subst", bnode("echo", bnode("$()", bnode("git rev-parse HEAD"))), "echo $(git rev-parse HEAD)"},
		// a wrapper wraps exactly what a container would compose, so a LIST inside
		// one is written with a join node — one rule, no special cases
		{"subshell", bnode("()", bnode(";", bnode("cd /tmp"), bnode("ls"))), "(cd /tmp ; ls)"},
		{
			"nested pipeline under a head",
			bnode("rg", bnode("-n"), bnode("func"), bnode("|", bnode("head -20"), bnode("wc -l"))),
			"rg -n func head -20 | wc -l",
		},
		{
			"tree of trees",
			bnode("|",
				bnode("rg", bnode("--hidden -n"), bnode(`"func .*Msg"`), bnode("tui/editor")),
				bnode("wc -l")),
			`rg --hidden -n "func .*Msg" tui/editor | wc -l`,
		},
		{"blank text and no kids", bnode("   "), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bashCompose(c.it, plain); got != c.want {
				t.Errorf("compose = %q, want %q", got, c.want)
			}
		})
	}
}

// TestBashComposeSkipsCompleted: completing a node comments it (and its subtree)
// out of the command, so a flag can be dropped without deleting it.
func TestBashComposeSkipsCompleted(t *testing.T) {
	it := bnode("rg", bnode("-n"), bnode("--hidden"), bnode("TODO"))
	it.children[1].completedAt = 1
	if got, want := bashCompose(it, plain), "rg -n TODO"; got != want {
		t.Errorf("compose = %q, want %q (completed child dropped)", got, want)
	}
	// a completed subtree takes its children with it
	sub := bnode("|", bnode("ls"), bnode("head", bnode("-5")))
	sub.children[1].completedAt = 1
	if got, want := bashCompose(sub, plain), "ls"; got != want {
		t.Errorf("compose = %q, want %q (completed subtree dropped)", got, want)
	}
	// completing the head silences the whole node
	it.completedAt = 1
	if got := bashCompose(it, plain); got != "" {
		t.Errorf("compose = %q, want empty (completed head)", got)
	}
}

// TestBashCommandResolvesChips: a chip in the tree runs as its real value and
// previews in its compact display form — no sentinels leak into either.
func TestBashCommandResolvesChips(t *testing.T) {
	m := &Model{chips: map[string]database.Chip{
		"c1": {ID: "c1", Kind: chipKindBash, Value: "date +%s"},
	}}
	it := bnode("echo", bnode(chipAnchor("c1")))

	if got, want := m.bashCommand(it), "echo date +%s"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	preview := bashPreview(it, m.chips)
	if want := "echo $ date +%s"; preview != want {
		t.Errorf("preview = %q, want %q", preview, want)
	}
	if strings.ContainsRune(preview, chipSentinel) {
		t.Errorf("preview leaked a chip sentinel: %q", preview)
	}
}

// TestBashRunsThisSubtreeOnly: alt+r on a sub-node composes from THAT node down,
// so running a branch runs just that part of the command.
func TestBashRunsThisSubtreeOnly(t *testing.T) {
	m := &Model{}
	root := bnode("|",
		bnode("rg", bnode("-n"), bnode("TODO")),
		bnode("wc -l"))

	if got, want := m.bashCommand(root), "rg -n TODO | wc -l"; got != want {
		t.Errorf("root command = %q, want %q", got, want)
	}
	if got, want := m.bashCommand(root.children[0]), "rg -n TODO"; got != want {
		t.Errorf("branch command = %q, want %q", got, want)
	}
}

// TestRunBashNodeEmptyIsNoop: an empty bash node flashes instead of spawning a
// shell that would run nothing.
func TestRunBashNodeEmptyIsNoop(t *testing.T) {
	m := newTestModel(80, "x")
	it := bnode("  ")
	if cmd := runBashNode(m, it); cmd != nil {
		t.Error("an empty bash node started a run")
	}
	if m.flash == "" {
		t.Error("an empty bash node ran silently — expected a flash")
	}
	if r := m.run(it.uuid); r != nil && r.cancel != nil {
		t.Error("an empty bash node left a live run")
	}
}

// TestBashRowLook: the row wears the bash chip's red "$" — always, fold or no
// fold — a parent hangs the composed command dim after its text, and a leaf
// hangs nothing (its text IS the command).
func TestBashRowLook(t *testing.T) {
	if g, col := bashGlyph(bnode("ls")); g != glyphBashPrompt || col != cRed {
		t.Errorf("leaf glyph = %q/%q, want $/red", g, col)
	}
	// the FOLD STATE is not part of it — folding a bash tree behaves like folding
	// any node, glyph included
	tree := bnode("|", bnode("ls"), bnode("wc -l"))
	tree.collapsed = true
	if g, _ := bashGlyph(tree); g != glyphBashPrompt {
		t.Errorf("folded tree glyph = %q, want $ (fold must not change the glyph)", g)
	}
	if tail := bashBodyTail(bnode("echo hi"), nil); tail != "" {
		t.Errorf("leaf tail = %q, want empty", tail)
	}
	tail := bashBodyTail(bnode("|", bnode("ls"), bnode("wc -l")), nil)
	if !strings.Contains(stripSGR(tail), "ls | wc -l") {
		t.Errorf("parent tail = %q, want the composed command", stripSGR(tail))
	}
	if !strings.HasPrefix(tail, cDim) {
		t.Errorf("parent tail should be dim: %q", tail)
	}
}

// bashRunRow builds a bash node with a run in hand — the state the row's "→"
// section reads — and syncs the per-frame tail map the way View does.
func bashRunRow(t *testing.T, running bool, out ...string) (*Model, *item) {
	t.Helper()
	m := newTestModel(80, "run me")
	it := m.rows[0].it
	it.uuid, it.typ = "u-run", database.TypeBash
	r := m.ensureRun(it.uuid)
	r.loaded = true
	if running {
		r.cancel = func() {}
		r.started = time.Now().Add(-3 * time.Second)
	}
	for _, l := range out {
		r.out = append(r.out, outLine{text: l})
	}
	m.syncRunTails()
	t.Cleanup(func() { runTails = nil })
	return m, it
}

// TestBashRowStreamsInItsTail: a bash node is the bash chip's tree form, so it
// streams the same way — the run's headline replaces the row's "→" section
// (newest line while running, first line once settled) and NOTHING hangs beneath
// the row. The composed-command preview is what a row with no run falls back to.
func TestBashRowStreamsInItsTail(t *testing.T) {
	m, it := bashRunRow(t, true, "step one", "step two", "step three")

	tail := stripSGR(bashBodyTail(it, nil))
	if want := "→ step three"; tail != want {
		t.Errorf("running tail = %q, want %q (the newest line)", tail, want)
	}
	// live means live: the tail wears the same sliding shimmer the running chip's
	// cell does, so the row itself says "working"
	if raw := bashBodyTail(it, nil); !strings.Contains(raw, "\x1b[38;2;") || strings.HasPrefix(raw, cDim) {
		t.Errorf("a running tail should shimmer, not sit dim: %q", raw)
	}
	// and it claims no band under the row — that space belongs to the outline
	_, bands := m.viewRenderRows(80)
	if under := stripSGR(strings.Join(bands[0], "\n")); strings.TrimSpace(under) != "" {
		t.Errorf("a running bash row drew a band underneath:\n%s", under)
	}

	// settled: the tail keeps the headline, the run's FIRST line
	m.run(it.uuid).cancel = nil
	m.syncRunTails()
	if got, want := stripSGR(bashBodyTail(it, nil)), "→ step one"; got != want {
		t.Errorf("settled tail = %q, want %q (the headline)", got, want)
	}
	if !strings.HasPrefix(bashBodyTail(it, nil), cDim) {
		t.Error("a settled tail should be dim")
	}
}

// TestBashTailFallsBackToTheCommand: with no run in hand the tree still explains
// itself — the "→" section is the composed command, as it was before any run.
func TestBashTailFallsBackToTheCommand(t *testing.T) {
	m, it := bashRunRow(t, false)
	m.run(it.uuid).out = nil
	m.syncRunTails()
	it.children = []*item{bnode("ls"), bnode("wc -l")}
	it.name = "|"
	if got := stripSGR(bashBodyTail(it, nil)); !strings.Contains(got, "ls | wc -l") {
		t.Errorf("tail = %q, want the composed command", got)
	}
}

// TestBashSpanColorTintsOperators: shell operators are yellow wherever they sit —
// an operator row and an inline redirect alike — and ordinary words are untouched.
func TestBashSpanColorTintsOperators(t *testing.T) {
	runes := []rune("go test 2>&1 | tee log")
	got := bashSpanColor(bnode("x"), runes)
	for i, r := range runes {
		want := ""
		switch {
		case i >= 8 && i < 12: // 2>&1
			want = cYellow
		case r == '|':
			want = cYellow
		}
		if got[i] != want {
			t.Errorf("rune %d (%q) color = %q, want %q", i, r, got[i], want)
		}
	}
}

// TestBashTypeRegistered: the type is in the registry with its run/view hooks and
// is offered by /type (it is in the canonical TypeOrder).
func TestBashTypeRegistered(t *testing.T) {
	nt := typeOf(database.TypeBash)
	if nt.key != database.TypeBash {
		t.Fatalf("bash type not registered (got key %q)", nt.key)
	}
	if nt.run == nil || nt.view == nil || nt.glyph == nil || nt.bodyTail == nil {
		t.Error("bash type is missing a hook (run/view/glyph/bodyTail)")
	}
	if !nt.inlineEditable {
		t.Error("a bash node must stay inline editable — its text is the command")
	}
	if !database.ValidTypes[database.TypeBash] {
		t.Error("bash is not an accepted node type")
	}
}

// TestBashSilentRunShimmers: a node that is RUNNING but has printed nothing yet
// still reads as alive — the row's tail is the shimmering "running…", not the dim
// composed-command preview. Guards the regression where a silent `sleep 30` sat
// static because the animation tick only started on the first output byte.
func TestBashSilentRunShimmers(t *testing.T) {
	m := newTestModel(80, "x")
	it := m.rows[0].it
	it.uuid, it.typ = "u-run", database.TypeBash
	it.name = "sleep 30"
	r := m.ensureRun(it.uuid)
	r.loaded = true
	r.cancel = func() {}
	r.started = time.Now().Add(-3 * time.Second)
	r.cmd = "sleep 30"
	m.syncRunTails()
	t.Cleanup(func() { runTails = nil })

	tail := bashBodyTail(it, nil)
	if !strings.Contains(tail, "\x1b") {
		t.Errorf("a silent running tail should shimmer, got %q", stripSGR(tail))
	}
	if !strings.Contains(stripSGR(tail), "running") {
		t.Errorf("silent running tail should say running…, got %q", stripSGR(tail))
	}
}

// TestBashChangedCmdDropsStaleTail: the tail belongs to the command that was RUN.
// When the node's composed command changes since the run, the old result is stale
// and the tail reverts to showing the newly composed command.
func TestBashChangedCmdDropsStaleTail(t *testing.T) {
	m := newTestModel(80, "x")
	it := m.rows[0].it
	it.uuid, it.typ = "u-run", database.TypeBash
	it.children = []*item{bnode("ls"), bnode("wc -l")}
	it.name = "|"
	r := m.ensureRun(it.uuid)
	r.loaded = true
	r.cmd = "ls | wc -l"
	r.out = append(r.out, outLine{text: "5"})
	m.syncRunTails()
	t.Cleanup(func() { runTails = nil })

	if got := stripSGR(bashBodyTail(it, nil)); !strings.Contains(got, "→ 5") {
		t.Errorf("unchanged cmd tail = %q, want the run result", got)
	}
	it.name = "&&"
	m.syncRunTails()
	got := stripSGR(bashBodyTail(it, nil))
	if strings.Contains(got, "5") {
		t.Errorf("changed cmd tail = %q, must drop the old result", got)
	}
	if !strings.Contains(got, "ls && wc -l") {
		t.Errorf("changed cmd tail = %q, want the new composed command", got)
	}
}

// TestBashRunningCountInToolbar: live shell runs tally in the status bar as a red
// "N running", like the suggestions count — a long silent command must still be
// visible in the bar.
func TestBashRunningCountInToolbar(t *testing.T) {
	m := newTestModel(80, "sleep 30")
	r1 := m.ensureRun(m.rows[0].it.uuid)
	r1.cancel = func() {}
	r1.started = time.Now().Add(-3 * time.Second)
	// a second live run on another id (a bash chip id, say)
	r2 := m.ensureRun("chip-1")
	r2.cancel = func() {}
	r2.started = time.Now().Add(-3 * time.Second)
	if n := m.runningCount(); n != 2 {
		t.Fatalf("runningCount = %d, want 2", n)
	}
	bar := stripSGR(strings.Join(m.bottomBar(79), "\n"))
	if !strings.Contains(bar, "2 bash running") {
		t.Errorf("bar = %q, want a 2 bash running tally", bar)
	}
}
