package operations

import (
	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/server/helpers"
	"github.com/lflow/lflow/pkg/server/permissions"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// GetNode retrieves a node for the given user
func GetNode(db *gorm.DB, uuid string, user *database.User) (database.Node, bool, error) {
	zeroNode := database.Node{}
	if !helpers.ValidateUUID(uuid) {
		return zeroNode, false, nil
	}

	var node database.Node
	err := db.Where("nodes.uuid = ? AND deleted = ?", uuid, false).Preload("User").Find(&node).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return zeroNode, false, nil
	} else if err != nil {
		return zeroNode, false, errors.Wrap(err, "finding node")
	}

	if ok := permissions.ViewNode(user, node); !ok {
		return zeroNode, false, nil
	}

	return node, true, nil
}
