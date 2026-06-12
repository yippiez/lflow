/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package editor

import (
	"time"

	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/utils"
	"github.com/pkg/errors"
)

// item is an in-memory outline node.
type item struct {
	uuid        string
	name        string
	note        string
	layout      string
	mirrorOf    string
	completedAt int64
	children    []*item
	parent      *item
	collapsed   bool
	isNew       bool
}

// snapshot captures a node's persisted state for change detection on save.
type snapshot struct {
	parentUUID  string
	rank        int
	name        string
	note        string
	layout      string
	mirrorOf    string
	completedAt int64
}

// tree is the in-memory model of the subtree being edited.
type tree struct {
	db        *database.DB
	root      *item
	snapshots map[string]snapshot
	deleted   []string // uuids of pre-existing nodes deleted in this session
	// resolved names for mirrors whose originals are outside the tree
	externalNames map[string]string
	byUUID        map[string]*item
}

// loadTree loads the subtree rooted at rootUUID into memory.
func loadTree(db *database.DB, rootUUID string) (*tree, error) {
	nodes, err := database.GetSubtree(db, rootUUID)
	if err != nil {
		return nil, err
	}

	t := &tree{
		db:            db,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{},
	}

	items := map[string]*item{}
	for _, n := range nodes {
		items[n.UUID] = &item{
			uuid:        n.UUID,
			name:        n.Name,
			note:        n.Note,
			layout:      n.Layout,
			mirrorOf:    n.MirrorOf,
			completedAt: n.CompletedAt,
		}
		t.snapshots[n.UUID] = snapshot{
			parentUUID:  n.ParentUUID,
			rank:        n.Rank,
			name:        n.Name,
			note:        n.Note,
			layout:      n.Layout,
			mirrorOf:    n.MirrorOf,
			completedAt: n.CompletedAt,
		}
	}

	for _, n := range nodes {
		it := items[n.UUID]
		if n.UUID == rootUUID {
			t.root = it
			continue
		}
		parent := items[n.ParentUUID]
		if parent == nil {
			parent = items[rootUUID]
		}
		it.parent = parent
		parent.children = append(parent.children, it)
	}

	t.byUUID = items

	// resolve mirror originals that live outside this subtree
	for _, it := range items {
		if it.mirrorOf == "" {
			continue
		}
		if _, inTree := items[it.mirrorOf]; inTree {
			continue
		}
		orig, err := database.GetNode(db, it.mirrorOf)
		if err == nil {
			t.externalNames[it.mirrorOf] = orig.Name
		} else {
			t.externalNames[it.mirrorOf] = "(missing)"
		}
	}

	return t, nil
}

// displayName resolves the visible name of an item: mirrors show the
// original's name (same node everywhere).
func (t *tree) displayName(it *item) string {
	if it.mirrorOf == "" {
		return it.name
	}
	if orig, ok := t.byUUID[it.mirrorOf]; ok {
		return orig.name
	}
	return t.externalNames[it.mirrorOf]
}

// row is a visible line of the outline.
type row struct {
	it     *item
	depth  int
	last   bool   // last child of its parent (elbow connector)
	branch []bool // for each ancestor level: does it have later siblings (draw │)
}

// visibleRows flattens the tree below viewRoot into displayable rows,
// honoring collapsed state. The view root itself is not a row.
func (t *tree) visibleRows(viewRoot *item) []row {
	var rows []row
	var walk func(it *item, depth int, branch []bool)
	walk = func(it *item, depth int, branch []bool) {
		for i, c := range it.children {
			last := i == len(it.children)-1
			rows = append(rows, row{it: c, depth: depth, last: last, branch: append([]bool(nil), branch...)})
			if !c.collapsed {
				walk(c, depth+1, append(branch, !last))
			}
		}
	}
	walk(viewRoot, 0, nil)
	return rows
}

// indexOf finds the index of it among its parent's children.
func indexOf(it *item) int {
	if it.parent == nil {
		return -1
	}
	for i, c := range it.parent.children {
		if c == it {
			return i
		}
	}
	return -1
}

// newItem creates a fresh local node.
func (t *tree) newItem() (*item, error) {
	uuid, err := utils.GenerateUUID()
	if err != nil {
		return nil, errors.Wrap(err, "generating uuid")
	}
	it := &item{uuid: uuid, layout: database.LayoutBullets, isNew: true}
	t.byUUID[uuid] = it
	return it, nil
}

// insertSiblingAfter inserts a new empty node after the given one.
func (t *tree) insertSiblingAfter(after *item) (*item, error) {
	it, err := t.newItem()
	if err != nil {
		return nil, err
	}
	parent := after.parent
	it.parent = parent
	idx := indexOf(after)
	parent.children = append(parent.children, nil)
	copy(parent.children[idx+2:], parent.children[idx+1:])
	parent.children[idx+1] = it
	return it, nil
}

// insertFirstChild inserts a new empty node as the first child of parent.
func (t *tree) insertFirstChild(parent *item) (*item, error) {
	it, err := t.newItem()
	if err != nil {
		return nil, err
	}
	it.parent = parent
	parent.children = append([]*item{it}, parent.children...)
	return it, nil
}

// remove detaches the item (and its subtree) from the tree and records
// pre-existing nodes for tombstoning on save.
func (t *tree) remove(it *item) {
	idx := indexOf(it)
	if idx < 0 {
		return
	}
	parent := it.parent
	parent.children = append(parent.children[:idx], parent.children[idx+1:]...)

	var collect func(x *item)
	collect = func(x *item) {
		if !x.isNew {
			t.deleted = append(t.deleted, x.uuid)
		}
		delete(t.byUUID, x.uuid)
		for _, c := range x.children {
			collect(c)
		}
	}
	collect(it)
}

// indent makes the item a child of its previous sibling.
func (t *tree) indent(it *item) bool {
	idx := indexOf(it)
	if idx <= 0 {
		return false
	}
	prev := it.parent.children[idx-1]
	it.parent.children = append(it.parent.children[:idx], it.parent.children[idx+1:]...)
	it.parent = prev
	prev.children = append(prev.children, it)
	prev.collapsed = false
	return true
}

// outdent makes the item the next sibling of its parent.
func (t *tree) outdent(it *item, viewRoot *item) bool {
	parent := it.parent
	if parent == nil || parent == viewRoot {
		return false
	}
	grandparent := parent.parent
	if grandparent == nil {
		return false
	}
	idx := indexOf(it)
	parent.children = append(parent.children[:idx], parent.children[idx+1:]...)

	pIdx := indexOf(parent)
	it.parent = grandparent
	grandparent.children = append(grandparent.children, nil)
	copy(grandparent.children[pIdx+2:], grandparent.children[pIdx+1:])
	grandparent.children[pIdx+1] = it
	return true
}

// move shifts the item up or down among its siblings.
func (t *tree) move(it *item, delta int) bool {
	idx := indexOf(it)
	if idx < 0 {
		return false
	}
	target := idx + delta
	if target < 0 || target >= len(it.parent.children) {
		return false
	}
	sibs := it.parent.children
	sibs[idx], sibs[target] = sibs[target], sibs[idx]
	return true
}

// reparent moves the item under a new parent (appended last).
func (t *tree) reparent(it *item, newParent *item) bool {
	// cycle check: newParent must not be inside it's subtree
	for p := newParent; p != nil; p = p.parent {
		if p == it {
			return false
		}
	}
	idx := indexOf(it)
	if idx < 0 {
		return false
	}
	it.parent.children = append(it.parent.children[:idx], it.parent.children[idx+1:]...)
	it.parent = newParent
	newParent.children = append(newParent.children, it)
	return true
}

// stats returns total node count below the view root and the edited count.
func (t *tree) stats() (total int, edited int) {
	var walk func(it *item)
	walk = func(it *item) {
		total++
		if it.isNew || t.changed(it) {
			edited++
		}
		for _, c := range it.children {
			walk(c)
		}
	}
	for _, c := range t.root.children {
		walk(c)
	}
	total++ // include root
	if t.changed(t.root) {
		edited++
	}
	return total, edited
}

// changed reports whether the item's content differs from its snapshot
// (structure changes are detected separately during save).
func (t *tree) changed(it *item) bool {
	if it.isNew {
		return true
	}
	s, ok := t.snapshots[it.uuid]
	if !ok {
		return true
	}
	return s.name != it.name || s.note != it.note || s.layout != it.layout ||
		s.mirrorOf != it.mirrorOf || s.completedAt != it.completedAt
}

// save writes the in-memory tree back to the database in one transaction.
// It returns the number of nodes that were inserted or updated.
func (t *tree) save() (int, error) {
	tx, err := t.db.Begin()
	if err != nil {
		return 0, errors.Wrap(err, "beginning a transaction")
	}

	now := time.Now().UnixNano()
	written := 0

	var walk func(it *item, parentUUID string, rank int) error
	walk = func(it *item, parentUUID string, rank int) error {
		s, existed := t.snapshots[it.uuid]
		structChanged := !existed || s.parentUUID != parentUUID || s.rank != rank

		if it.isNew {
			n := database.Node{
				UUID:        it.uuid,
				ParentUUID:  parentUUID,
				Rank:        rank,
				Name:        it.name,
				Note:        it.note,
				Layout:      it.layout,
				MirrorOf:    it.mirrorOf,
				CompletedAt: it.completedAt,
				AddedOn:     now,
				EditedOn:    now,
				Dirty:       true,
			}
			if err := n.Insert(tx); err != nil {
				return err
			}
			written++
		} else if t.changed(it) || structChanged {
			if _, err := tx.Exec(`UPDATE nodes SET parent_uuid = ?, rank = ?, name = ?, note = ?, layout = ?,
				mirror_of = ?, completed_at = ?, edited_on = ?, dirty = 1 WHERE uuid = ?`,
				parentUUID, rank, it.name, it.note, it.layout, it.mirrorOf, it.completedAt, now, it.uuid); err != nil {
				return errors.Wrapf(err, "updating node %s", it.uuid)
			}
			written++
		}

		for i, c := range it.children {
			if err := walk(c, it.uuid, i); err != nil {
				return err
			}
		}
		return nil
	}

	// the root's own parent/rank are not managed by this editor
	if t.changed(t.root) {
		if _, err := tx.Exec(`UPDATE nodes SET name = ?, note = ?, layout = ?, completed_at = ?, edited_on = ?, dirty = 1 WHERE uuid = ?`,
			t.root.name, t.root.note, t.root.layout, t.root.completedAt, now, t.root.uuid); err != nil {
			tx.Rollback()
			return 0, errors.Wrap(err, "updating root node")
		}
		written++
	}
	for i, c := range t.root.children {
		if err := walk(c, t.root.uuid, i); err != nil {
			tx.Rollback()
			return 0, err
		}
	}

	for _, uuid := range t.deleted {
		if _, err := tx.Exec("UPDATE nodes SET deleted = 1, dirty = 1 WHERE uuid = ?", uuid); err != nil {
			tx.Rollback()
			return 0, errors.Wrapf(err, "tombstoning node %s", uuid)
		}
		written++
	}

	if err := tx.Commit(); err != nil {
		return 0, errors.Wrap(err, "committing transaction")
	}

	// refresh snapshots so a second save is a no-op
	t.refreshSnapshots()
	t.deleted = nil

	return written, nil
}

func (t *tree) refreshSnapshots() {
	t.snapshots = map[string]snapshot{}
	var walk func(it *item, parentUUID string, rank int)
	walk = func(it *item, parentUUID string, rank int) {
		it.isNew = false
		t.snapshots[it.uuid] = snapshot{
			parentUUID:  parentUUID,
			rank:        rank,
			name:        it.name,
			note:        it.note,
			layout:      it.layout,
			mirrorOf:    it.mirrorOf,
			completedAt: it.completedAt,
		}
		for i, c := range it.children {
			walk(c, it.uuid, i)
		}
	}
	walk(t.root, "", 0)
}
