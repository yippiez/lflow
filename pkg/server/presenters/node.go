package presenters

import (
	"time"

	"github.com/lflow/lflow/pkg/server/database"
)

// Node is a result of PresentNode
type Node struct {
	UUID        string    `json:"uuid"`
	ParentUUID  string    `json:"parent_uuid"`
	Rank        int       `json:"rank"`
	Name        string    `json:"name"`
	Note        string    `json:"note"`
	Type        string    `json:"type"`
	MirrorOf    string    `json:"mirror_of"`
	CompletedAt int64     `json:"completed_at"`
	AddedOn     int64     `json:"added_on"`
	EditedOn    int64     `json:"edited_on"`
	USN         int       `json:"usn"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PresentNode presents a node
func PresentNode(node database.Node) Node {
	return Node{
		UUID:        node.UUID,
		ParentUUID:  node.ParentUUID,
		Rank:        node.Rank,
		Name:        node.Name,
		Note:        node.Note,
		Type:        node.Type,
		MirrorOf:    node.MirrorOf,
		CompletedAt: node.CompletedAt,
		AddedOn:     node.AddedOn,
		EditedOn:    node.EditedOn,
		USN:         node.USN,
		CreatedAt:   FormatTS(node.CreatedAt),
		UpdatedAt:   FormatTS(node.UpdatedAt),
	}
}

// PresentNodes presents nodes
func PresentNodes(nodes []database.Node) []Node {
	ret := []Node{}

	for _, node := range nodes {
		ret = append(ret, PresentNode(node))
	}

	return ret
}
