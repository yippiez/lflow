package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
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
		{"head", bnode("rg", bnode("-n"), bnode("TODO"), bnode("pkg/tui")), "rg -n TODO pkg/tui"},
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
				bnode("rg", bnode("--hidden -n"), bnode(`"func .*Msg"`), bnode("pkg/tui")),
				bnode("wc -l")),
			`rg --hidden -n "func .*Msg" pkg/tui | wc -l`,
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
		"c1": {ID: "c1", Kind: chipKindCmd, Value: "date +%s"},
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

// TestBashRowLook: the row wears the cmd chip's red "$" — always, fold or no
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

