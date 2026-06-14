package app

import (
	"errors"

	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/server/helpers"
	pkgErrors "github.com/pkg/errors"
	"gorm.io/gorm"
)

// NodeParams carries the full client-side state of a node.
type NodeParams struct {
	ParentUUID  string `schema:"parent_uuid" json:"parent_uuid"`
	Rank        int    `schema:"rank" json:"rank"`
	Name        string `schema:"name" json:"name"`
	Note        string `schema:"note" json:"note"`
	Layout      string `schema:"layout" json:"layout"`
	MirrorOf    string `schema:"mirror_of" json:"mirror_of"`
	CompletedAt int64  `schema:"completed_at" json:"completed_at"`
	AddedOn     *int64 `schema:"added_on" json:"added_on"`
	EditedOn    *int64 `schema:"edited_on" json:"edited_on"`
}

// validLayouts is the set of accepted layout values.
var validLayouts = map[string]bool{
	"bullets": true,
	"todo":    true,
	"h1":      true,
	"h2":      true,
	"h3":      true,
	"code":    true,
	"quote":   true,
}

// ErrInvalidLayout is returned when the layout value is not recognized.
var ErrInvalidLayout = errors.New("invalid layout")

// ValidateNodeParams validates the node payload.
func ValidateNodeParams(p NodeParams) error {
	if p.Layout != "" && !validLayouts[p.Layout] {
		return ErrInvalidLayout
	}
	return nil
}

// CreateNode creates a node with the next usn and updates the user's max_usn.
func (a *App) CreateNode(user database.User, p NodeParams, client string) (database.Node, error) {
	tx := a.DB.Begin()

	nextUSN, err := incrementUserUSN(tx, user.ID)
	if err != nil {
		tx.Rollback()
		return database.Node{}, pkgErrors.Wrap(err, "incrementing user max_usn")
	}

	var addedOn int64
	if p.AddedOn == nil {
		addedOn = a.Clock.Now().UnixNano()
	} else {
		addedOn = *p.AddedOn
	}

	var editedOn int64
	if p.EditedOn != nil {
		editedOn = *p.EditedOn
	}

	layout := p.Layout
	if layout == "" {
		layout = "bullets"
	}

	uuid, err := helpers.GenUUID()
	if err != nil {
		tx.Rollback()
		return database.Node{}, err
	}

	node := database.Node{
		UUID:        uuid,
		UserID:      user.ID,
		ParentUUID:  p.ParentUUID,
		Rank:        p.Rank,
		Name:        p.Name,
		Note:        p.Note,
		Layout:      layout,
		MirrorOf:    p.MirrorOf,
		CompletedAt: p.CompletedAt,
		AddedOn:     addedOn,
		EditedOn:    editedOn,
		USN:         nextUSN,
		Client:      client,
	}
	if err := tx.Create(&node).Error; err != nil {
		tx.Rollback()
		return node, pkgErrors.Wrap(err, "inserting node")
	}

	tx.Commit()

	return node, nil
}

// UpdateNode applies the full node state with the next usn and updates the
// user's max_usn.
func (a *App) UpdateNode(tx *gorm.DB, user database.User, node database.Node, p NodeParams) (database.Node, error) {
	nextUSN, err := incrementUserUSN(tx, user.ID)
	if err != nil {
		return node, pkgErrors.Wrap(err, "incrementing user max_usn")
	}

	node.ParentUUID = p.ParentUUID
	node.Rank = p.Rank
	node.Name = p.Name
	node.Note = p.Note
	if p.Layout != "" {
		node.Layout = p.Layout
	}
	node.MirrorOf = p.MirrorOf
	node.CompletedAt = p.CompletedAt

	node.USN = nextUSN
	if p.EditedOn != nil {
		node.EditedOn = *p.EditedOn
	} else {
		node.EditedOn = a.Clock.Now().UnixNano()
	}
	node.Deleted = false

	if err := tx.Save(&node).Error; err != nil {
		return node, pkgErrors.Wrap(err, "editing node")
	}

	return node, nil
}

// DeleteNode marks a node deleted with the next usn and updates the user's max_usn
func (a *App) DeleteNode(tx *gorm.DB, user database.User, node database.Node) (database.Node, error) {
	nextUSN, err := incrementUserUSN(tx, user.ID)
	if err != nil {
		return node, pkgErrors.Wrap(err, "incrementing user max_usn")
	}

	if err := tx.Model(&node).
		Updates(map[string]interface{}{
			"usn":     nextUSN,
			"deleted": true,
			"name":    "",
			"note":    "",
		}).Error; err != nil {
		return node, pkgErrors.Wrap(err, "deleting node")
	}

	return node, nil
}

// GetNodesParams is params for finding nodes
type GetNodesParams struct {
	Page    int
	Search  string
	PerPage int
}

// GetNodesResult is the result of getting nodes
type GetNodesResult struct {
	Nodes []database.Node
	Total int64
}

func getNodesBaseQuery(db *gorm.DB, userID int, q GetNodesParams) *gorm.DB {
	conn := db.Where(
		"nodes.user_id = ? AND nodes.deleted = ?",
		userID, false,
	)

	if q.Search != "" {
		conn = conn.Joins("INNER JOIN nodes_fts ON nodes_fts.rowid = nodes.id").
			Where("nodes_fts MATCH ?", q.Search)
	}

	return conn
}

// GetNodes returns a list of matching nodes
func (a *App) GetNodes(userID int, params GetNodesParams) (GetNodesResult, error) {
	conn := getNodesBaseQuery(a.DB, userID, params)

	var total int64
	if err := conn.Model(database.Node{}).Count(&total).Error; err != nil {
		return GetNodesResult{}, pkgErrors.Wrap(err, "counting total")
	}

	nodes := []database.Node{}
	if total != 0 {
		conn = conn.Order("nodes.parent_uuid ASC, nodes.rank ASC, nodes.id ASC")
		if params.Page > 0 && params.PerPage > 0 {
			offset := params.PerPage * (params.Page - 1)
			conn = conn.Offset(offset)
		}
		if params.PerPage > 0 {
			conn = conn.Limit(params.PerPage)
		}

		if err := conn.Find(&nodes).Error; err != nil {
			return GetNodesResult{}, pkgErrors.Wrap(err, "finding nodes")
		}
	}

	return GetNodesResult{Nodes: nodes, Total: total}, nil
}
