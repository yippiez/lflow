package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func TestPeelCodeFence(t *testing.T) {
	code, lang := peelCodeFence("```python\nb = sum(xs)\nprint(b)\n```")
	if lang != "python" || code != "b = sum(xs)\nprint(b)" {
		t.Fatalf("fence parse: lang=%q code=%q", lang, code)
	}
	code, lang = peelCodeFence("here you go:\n```go\nfunc S() int { return 1 }\n```\nenjoy")
	if lang != "go" || !strings.Contains(code, "func S()") || strings.Contains(code, "enjoy") {
		t.Fatalf("fence with prose: lang=%q code=%q", lang, code)
	}
	code, lang = peelCodeFence("plain = 1")
	if lang != "" || code != "plain = 1" {
		t.Fatalf("unfenced passthrough: %q %q", code, lang)
	}
}

func TestNCColorLine(t *testing.T) {
	if got := ncColorLine("# a comment"); !strings.HasPrefix(got, cDim) {
		t.Fatalf("comment must dim: %q", got)
	}
	if got := ncColorLine(`x = "hi" + 42`); !strings.Contains(got, styleColorCode["orange"]) ||
		!strings.Contains(got, styleColorCode["green"]) {
		t.Fatalf("string+number must color: %q", got)
	}
	if got := ncColorLine("def train(inputs):"); !strings.Contains(got, cAccent+"def"+cReset) {
		t.Fatalf("keyword must color: %q", got)
	}
}

// TestNLPComputeGeneratesCode: alt+r runs the (mock) generator; the reply
// lands as the cell's code version and the cwd pins on first run.
func TestNLPComputeGeneratesCode(t *testing.T) {
	m, _, n1 := newAgentTestModel(t)
	n1.typ = database.TypeNLPCompute
	n1.name = "sum the inputs and store the result as b"

	cmd := runNLPCompute(m, n1)
	if cmd == nil {
		t.Fatalf("run must start: %s", m.flash)
	}
	if !ncStateOf(m, n1).busy {
		t.Fatal("cell must be busy while generating")
	}
	msg := cmd()
	for i := 0; i < 60; i++ {
		ev, ok := msg.(ncEvMsg)
		if !ok {
			t.Fatalf("unexpected msg %T", msg)
		}
		_, next := m.handleNCEvent(ev)
		if next == nil {
			break
		}
		msg = next()
	}

	d := m.ncLoad(n1.uuid)
	if strings.TrimSpace(d.Code) == "" {
		t.Fatal("the reply must land as the cell's code")
	}
	if d.Cwd == "" {
		t.Fatal("the cell must pin its cwd on first run")
	}
	if ncStateOf(m, n1).busy {
		t.Fatal("cell must be idle after done")
	}

	// the code version opens once code exists
	if !(ncView{}).Enter(m, n1) {
		t.Fatal("alt+e must open the code version")
	}
	bands := (ncView{}).Bands(m, n1, "", 100, 0, 30, true)
	if len(bands) < 2 {
		t.Fatalf("code bands missing: %v", bands)
	}
}
