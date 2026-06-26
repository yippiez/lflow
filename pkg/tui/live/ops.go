package live

import (
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/utils"
	"github.com/pkg/errors"
)

// op is an edit request from a client. Only the fields relevant to its Op are
// populated; the rest are zero values.
type op struct {
	Op         string `json:"op"`
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid"`
	Name       string `json:"name"`
	NodeType   string `json:"nodeType"`
	Collapsed  bool   `json:"collapsed"`
	Rank       int    `json:"rank"`
}

// apply mutates the outline per the op. Callers serialize apply() so concurrent
// clients can't race on the DB. Style/link/readonly are untouched here — the
// first build only edits structure and text.
func (s *Server) apply(o op) error {
	db := s.db
	now := time.Now().UnixNano()

	switch o.Op {
	case "update_name":
		n, err := database.GetNode(db, o.UUID)
		if err != nil {
			return err
		}
		n.Name = o.Name
		n.EditedOn = now
		n.Dirty = true
		return n.Update(db)

	case "toggle_done":
		n, err := database.GetNode(db, o.UUID)
		if err != nil {
			return err
		}
		if n.CompletedAt > 0 {
			n.CompletedAt = 0
		} else {
			n.CompletedAt = now
		}
		n.EditedOn = now
		n.Dirty = true
		return n.Update(db)

	case "set_collapsed":
		// view-state only: never touches dirty/usn/edited_on (see SetCollapsed).
		return database.SetCollapsed(db, o.UUID, o.Collapsed)

	case "add":
		parent := o.ParentUUID
		if parent == "" {
			parent = database.RootUUID
		}
		rank, err := database.NextRank(db, parent)
		if err != nil {
			return err
		}
		typ := o.NodeType
		if !database.ValidTypes[typ] {
			typ = database.TypeBullets
		}
		uuid, err := utils.GenerateUUID()
		if err != nil {
			return errors.Wrap(err, "generating uuid")
		}
		n := database.Node{
			UUID:       uuid,
			ParentUUID: parent,
			Rank:       rank,
			Name:       o.Name,
			Type:       typ,
			AddedOn:    now,
			EditedOn:   now,
			Dirty:      true,
		}
		return n.Insert(db)

	case "delete":
		_, err := database.MarkSubtreeDeleted(db, o.UUID)
		return err

	case "move":
		n, err := database.GetNode(db, o.UUID)
		if err != nil {
			return err
		}
		if o.ParentUUID != "" {
			n.ParentUUID = o.ParentUUID
		}
		n.Rank = o.Rank
		n.EditedOn = now
		n.Dirty = true
		return n.Update(db)

	default:
		return errors.Errorf("unknown op %q", o.Op)
	}
}
