package editor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Harvesting a finished worker: pressing Enter on it materializes its deliverable
// into the notebook. The deliverable is an OUTLINE (the finish_worker nodes), never
// markdown — we read the structure directly and build nodes from it. No markdown is
// ever parsed (it would leak markup into stored text).

// deliverNode mirrors the finish_worker outline node shape. Type lets the agent
// return custom node formats (bash, code, …); unknown/empty → a plain bullet.
type deliverNode struct {
	Text     string        `json:"text"`
	Note     string        `json:"note"`
	Type     string        `json:"type"`
	Children []deliverNode `json:"children"`
}

// deliverTypeMap maps agent-provided type names (and a few aliases) to node types
// agents are allowed to emit. worker/voice/query are excluded (local/runtime types).
var deliverTypeMap = map[string]string{
	"bullet": database.TypeBullets, "bullets": database.TypeBullets,
	"todo":    database.TypeTodo,
	"h1":      database.TypeH1, "h2": database.TypeH2, "h3": database.TypeH3,
	"heading": database.TypeH1,
	"code":    database.TypeCode,
	"quote":   database.TypeQuote,
	"bash":    database.TypeBash,
	"json":    database.TypeJSON,
}

// deliverType resolves an agent-provided type to an allowed node type (bullets default).
func deliverType(s string) string {
	if t, ok := deliverTypeMap[strings.ToLower(strings.TrimSpace(s))]; ok {
		return t
	}
	return database.TypeBullets
}

// parseDeliverNodes decodes finish_worker nodes JSON into deliverNode structs
// (tolerating a single bare object). Used by both harvest and the Final preview.
func parseDeliverNodes(nodesJSON string) []deliverNode {
	nodesJSON = strings.TrimSpace(nodesJSON)
	if nodesJSON == "" {
		return nil
	}
	var nodes []deliverNode
	if json.Unmarshal([]byte(nodesJSON), &nodes) != nil {
		var one deliverNode
		if json.Unmarshal([]byte(nodesJSON), &one) != nil {
			return nil
		}
		nodes = []deliverNode{one}
	}
	return nodes
}

// parseDeliverable turns the finish_worker nodes JSON into a forest of new items
// registered in t. Roots are returned with parent unset; the caller attaches them.
func parseDeliverable(t *tree, nodesJSON string) []*item {
	return buildDeliverItems(t, parseDeliverNodes(nodesJSON))
}

func buildDeliverItems(t *tree, nodes []deliverNode) []*item {
	var out []*item
	for _, n := range nodes {
		if strings.TrimSpace(n.Text) == "" && len(n.Children) == 0 {
			continue
		}
		it, err := t.newItem()
		if err != nil {
			continue
		}
		it.name = strings.TrimSpace(n.Text)
		it.note = strings.TrimSpace(n.Note)
		it.typ = deliverType(n.Type) // custom node format (bash, code, …)
		for _, c := range buildDeliverItems(t, n.Children) {
			c.parent = it
			it.children = append(it.children, c)
		}
		out = append(out, it)
	}
	return out
}

// parseOutlineText turns a plain multi-line outline (each line a node, two spaces
// of indent = one level of nesting) into new items registered in t. Used by the
// steer composer — plain text, never markdown.
func parseOutlineText(t *tree, text string) []*item {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var roots []*item
	type frame struct {
		depth int
		it    *item
	}
	var stack []frame
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		depth := (len([]rune(raw)) - len([]rune(trimmed))) / 2
		it, err := t.newItem()
		if err != nil {
			continue
		}
		it.name = strings.TrimSpace(trimmed)
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, it)
		} else {
			p := stack[len(stack)-1].it
			it.parent = p
			p.children = append(p.children, it)
		}
		stack = append(stack, frame{depth, it})
	}
	return roots
}

// harvestWorker materializes a finished worker's deliverable into the notebook
// (under the main view root) as outline nodes. Returns false if there is nothing
// to harvest. The spent worker is left in temp as a receipt.
func (m *Model) harvestWorker(it *item) bool {
	data := m.workerDeliverable[it.uuid]
	if strings.TrimSpace(data) == "" {
		return false
	}
	mainTree := m.mainStash.tree
	if mainTree == nil {
		return false
	}
	parent := mainTree.root
	if n := len(m.mainStash.viewStack); n > 0 {
		parent = m.mainStash.viewStack[n-1]
	}
	items := parseDeliverable(mainTree, data)
	if len(items) == 0 {
		return false
	}
	for _, n := range items {
		n.parent = parent
		parent.children = append(parent.children, n)
	}
	m.unsaved = true
	m.flash = fmt.Sprintf("harvested %d", len(items))
	return true
}
