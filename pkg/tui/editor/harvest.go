package editor

import (
	"fmt"
	"regexp"
	"strings"
)

// Harvesting a finished worker: pressing Enter on it materializes its deliverable
// (the finish_worker markdown) into the notebook. The markdown is parsed into
// nodes — plain prose becomes one node, bulleted/nested markdown becomes a
// subtree — so the shape follows the model's output (pchain-style).

var mdBulletRe = regexp.MustCompile(`^(\s*)(?:[-*+]|\d+\.)\s+(.*)$`)
var mdHeadingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

// parseMarkdownItems turns deliverable markdown into a forest of new items
// registered in t (so t.newItem assigns uuids / isNew). Roots are returned with
// parent unset; the caller attaches them.
func parseMarkdownItems(t *tree, md string) []*item {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	lines := strings.Split(md, "\n")

	structured := false
	for _, l := range lines {
		if mdBulletRe.MatchString(l) || mdHeadingRe.MatchString(strings.TrimSpace(l)) {
			structured = true
			break
		}
	}

	// plain prose → a single node: first line is the name, the rest its note
	if !structured {
		text := strings.TrimSpace(md)
		if text == "" {
			return nil
		}
		parts := strings.SplitN(text, "\n", 2)
		it, err := t.newItem()
		if err != nil {
			return nil
		}
		it.name = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			it.note = strings.TrimSpace(parts[1])
		}
		return []*item{it}
	}

	var roots []*item
	type frame struct {
		depth int
		it    *item
	}
	var stack []frame
	headingBase := 0 // bullets after a heading nest under it

	attach := func(it *item, depth int) {
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

	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if hm := mdHeadingRe.FindStringSubmatch(strings.TrimSpace(raw)); hm != nil {
			depth := len(hm[1]) - 1 // # = 0, ## = 1, …
			it, err := t.newItem()
			if err != nil {
				continue
			}
			it.name = strings.TrimSpace(hm[2])
			attach(it, depth)
			headingBase = depth + 1
			continue
		}
		if bm := mdBulletRe.FindStringSubmatch(raw); bm != nil {
			indent := len(strings.ReplaceAll(bm[1], "\t", "  ")) / 2
			it, err := t.newItem()
			if err != nil {
				continue
			}
			it.name = strings.TrimSpace(bm[2])
			attach(it, headingBase+indent)
			continue
		}
		// a plain continuation line: fold into the current node's note
		line := strings.TrimSpace(raw)
		if len(stack) > 0 {
			last := stack[len(stack)-1].it
			if last.note == "" {
				last.note = line
			} else {
				last.note += "\n" + line
			}
		} else {
			it, err := t.newItem()
			if err != nil {
				continue
			}
			it.name = line
			attach(it, 0)
		}
	}
	return roots
}

// harvestWorker materializes a finished worker's deliverable into the notebook
// (under the main view root) as parsed nodes. Returns false if there is nothing
// to harvest. The spent worker is left in temp as a receipt.
func (m *Model) harvestWorker(it *item) bool {
	md := m.workerDeliverable[it.uuid]
	if strings.TrimSpace(md) == "" {
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
	items := parseMarkdownItems(mainTree, md)
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
