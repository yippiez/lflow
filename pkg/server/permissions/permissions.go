package permissions

import (
	"github.com/lflow/lflow/pkg/server/database"
)

// ViewNode checks if the given user can view the given node
func ViewNode(user *database.User, node database.Node) bool {
	if user == nil {
		return false
	}
	if node.UserID == 0 {
		return false
	}

	return node.UserID == user.ID
}
