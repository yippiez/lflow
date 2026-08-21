package editor

import (
	"fmt"
	"testing"
	"time"

	"github.com/lflow/lflow/tui/database"
)

// benchTree builds the outline both editors are benchmarked on. The SHAPE IS
// A CONTRACT shared with repl's own frame benchmark
// (beyin-monorepo/packages/repl/src/tui/frame_bench_test.go): 120 sections,
// every third folded, nine children each cycling depth 1-2-3 and the four
// flavours — plain note with **bold**, stamped log, note with a link and a
// date, bash command — so the two editors are timed drawing the same outline.
func benchTree() *Model {
	root := &item{}
	t := &tree{
		root:          root,
		byUUID:        map[string]*item{},
		externalNames: map[string]string{},
	}

	attach := func(parent *item, it *item) *item {
		it.parent = parent
		parent.children = append(parent.children, it)
		return it
	}

	for i := 0; i < 120; i++ {
		section := attach(root, &item{
			name:      fmt.Sprintf("section %d #topic-%d", i, i%7),
			collapsed: i%3 == 0,
		})
		parents := []*item{section, nil, nil}
		for j := 0; j < 9; j++ {
			depth := 1 + j%3
			node := &item{}
			switch j % 4 {
			case 0:
				node.name = fmt.Sprintf("a note about the sweep %d with **bold** words", j)
			case 1:
				node.typ = database.TypeLog
				node.name = "ran the ablation · seed 7"
				node.addedOn = time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC).UnixNano()
			case 2:
				node.name = "see [the plan](lflow://node/a1b2c3d4) from 2026-08-19"
			case 3:
				node.typ = database.TypeBash
				node.name = "fd upload_s3.py"
			}
			attach(parents[depth-1], node)
			if depth < 3 {
				parents[depth] = node
			}
		}
	}

	m := &Model{tree: t, viewStack: []*item{root}, width: 170, height: 45}
	m.refreshRows()
	return m
}

// BenchmarkFrame is one redraw of the outline — the View pass alone.
func BenchmarkFrame(b *testing.B) {
	m := benchTree()
	m.cursor = len(m.rows) / 2

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.View()
	}
}

// BenchmarkFrameWalk is a held-down arrow key: the update AND the redraw,
// which is what a user feels as movement.
func BenchmarkFrameWalk(b *testing.B) {
	m := benchTree()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(key("down"))
		m.View()
	}
}
