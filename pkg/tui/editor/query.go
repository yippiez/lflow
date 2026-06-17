package editor

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A codebase live-query node: its name is a ripgrep pattern; alt+r searches the
// codebase (cwd) and lists the findings as a read-only band beneath the node.
// Results are ephemeral (in-memory, never persisted/synced), regenerated on run.

const queryMaxHits = 200

type queryDoneMsg struct {
	uuid  string
	lines []outLine
}

// rgEvent is one line of `rg --json` output.
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path  struct{ Text string } `json:"path"`
		Lines struct{ Text string } `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

func runQuery(m *Model, it *item) tea.Cmd {
	pattern := strings.TrimSpace(it.name)
	uuid := it.uuid
	if pattern == "" {
		return func() tea.Msg { return queryDoneMsg{uuid, []outLine{{text: "type a pattern, then alt+r"}}} }
	}
	return func() tea.Msg {
		out, err := exec.Command("rg", "--json", "--max-count", "20", "--", pattern).Output()
		if err != nil && len(out) == 0 {
			// rg exits 1 with no output when there are no matches
			return queryDoneMsg{uuid, []outLine{{text: "no matches"}}}
		}
		var lines []outLine
		for _, ln := range strings.Split(string(out), "\n") {
			if ln == "" {
				continue
			}
			var ev rgEvent
			if json.Unmarshal([]byte(ln), &ev) != nil || ev.Type != "match" {
				continue
			}
			text := strings.TrimRight(ev.Data.Lines.Text, "\n")
			text = strings.TrimSpace(text)
			lines = append(lines, outLine{text: fmt.Sprintf("%s:%d  %s", ev.Data.Path.Text, ev.Data.LineNumber, text)})
			if len(lines) >= queryMaxHits {
				break
			}
		}
		if len(lines) == 0 {
			lines = []outLine{{text: "no matches"}}
		}
		return queryDoneMsg{uuid, lines}
	}
}
