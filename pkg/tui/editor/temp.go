package editor

import "github.com/lflow/lflow/pkg/tui/database"

// The Temporary Domain is an ephemeral scratch outline: a second tree with a nil
// db, so it is never persisted or synced. alt+t swaps the editor into it (and
// back). In the main view a muted-gray dashed outline below the footer is the
// access affordance; everything edits exactly like the main outline, but is gone
// when you quit.

type tempStash struct {
	tree      *tree
	cursor    int
	caret     int
	viewStack []*item
	ancestors []string
}

func (m *Model) toggleTemp() {
	if m.tempActive {
		m.exitTemp()
	} else {
		m.enterTemp()
	}
}

func (m *Model) enterTemp() {
	if m.tempTree == nil {
		root := &item{uuid: "temp-root", typ: database.TypeBullets}
		m.tempTree = &tree{
			root:          root,
			snapshots:     map[string]snapshot{},
			externalNames: map[string]string{},
			byUUID:        map[string]*item{root.uuid: root},
		} // db is nil → save() is a no-op
	}
	m.mainStash = tempStash{tree: m.tree, cursor: m.cursor, caret: m.caret, viewStack: m.viewStack, ancestors: m.ancestors}
	m.tree = m.tempTree
	m.viewStack = []*item{m.tempTree.root}
	m.ancestors = nil
	m.cursor = 0
	m.caret = 0
	m.tempActive = true
	m.refreshRows()
	if len(m.rows) > 0 {
		m.caret = len([]rune(m.rows[0].it.name))
	}
}

func (m *Model) exitTemp() {
	m.tempTree = m.tree // keep the scratch content in-memory for this session
	s := m.mainStash
	m.tree = s.tree
	m.viewStack = s.viewStack
	m.ancestors = s.ancestors
	m.cursor = s.cursor
	m.caret = s.caret
	m.tempActive = false
	m.refreshRows()
	m.clampCursor()
}
