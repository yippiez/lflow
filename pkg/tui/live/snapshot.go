package live

import (
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/pkg/errors"
)

// wireNode is the JSON shape of a node sent to clients. It mirrors
// database.Node but drops sync bookkeeping (usn, dirty, deleted) the client
// has no use for, and uses camelCase keys for the JS client.
type wireNode struct {
	UUID        string `json:"uuid"`
	ParentUUID  string `json:"parentUuid"`
	Rank        int    `json:"rank"`
	Name        string `json:"name"`
	Note        string `json:"note"`
	Type        string `json:"type"`
	Style       string `json:"style"`
	MirrorOf    string `json:"mirrorOf"`
	LinkTo      string `json:"linkTo"`
	CompletedAt int64  `json:"completedAt"`
	Collapsed   bool   `json:"collapsed"`
	Readonly    bool   `json:"readonly"`
	AddedOn     int64  `json:"addedOn"`
	EditedOn    int64  `json:"editedOn"`
}

// wireChip is the JSON shape of a chip. The client splits a node's Name on the
// chip anchor sentinel and looks chips up by id to render them.
type wireChip struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// snapshot is the full outline state pushed on connect and after every change.
// Sending the whole tree keeps the protocol trivial; deltas are a later
// optimization once the forest is large enough to matter.
type snapshot struct {
	Type  string              `json:"type"` // always "snapshot"
	Root  string              `json:"root"`
	Nodes []wireNode          `json:"nodes"`
	Chips map[string]wireChip `json:"chips"`
}

// buildSnapshot reads the live outline (root subtree) plus the chip store and
// returns the wire snapshot. The Temporary Domain is excluded for now.
func buildSnapshot(db *database.DB) (snapshot, error) {
	if err := database.EnsureRoot(db); err != nil {
		return snapshot{}, err
	}

	subtree, err := database.GetSubtree(db, database.RootUUID)
	if err != nil {
		return snapshot{}, errors.Wrap(err, "loading root subtree")
	}

	nodes := make([]wireNode, 0, len(subtree))
	for _, n := range subtree {
		nodes = append(nodes, wireNode{
			UUID:        n.UUID,
			ParentUUID:  n.ParentUUID,
			Rank:        n.Rank,
			Name:        n.Name,
			Note:        n.Note,
			Type:        n.Type,
			Style:       n.Style,
			MirrorOf:    n.MirrorOf,
			LinkTo:      n.LinkTo,
			CompletedAt: n.CompletedAt,
			Collapsed:   n.Collapsed,
			Readonly:    n.Readonly,
			AddedOn:     n.AddedOn,
			EditedOn:    n.EditedOn,
		})
	}

	chips, err := database.LoadChips(db)
	if err != nil {
		return snapshot{}, errors.Wrap(err, "loading chips")
	}
	wireChips := make(map[string]wireChip, len(chips))
	for id, c := range chips {
		wireChips[id] = wireChip{Kind: c.Kind, Value: c.Value}
	}

	return snapshot{
		Type:  "snapshot",
		Root:  database.RootUUID,
		Nodes: nodes,
		Chips: wireChips,
	}, nil
}
