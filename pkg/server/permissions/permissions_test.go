package permissions

import (
	"testing"

	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/server/testutils"
	"github.com/lflow/lflow/pkg/shared/assert"
)

func TestViewNode(t *testing.T) {
	db := testutils.InitMemoryDB(t)

	user := testutils.SetupUserData(db, "user@test.com", "password123")
	anotherUser := testutils.SetupUserData(db, "another@test.com", "password123")

	node := database.Node{
		UUID:   testutils.MustUUID(t),
		UserID: user.ID,
		Name:   "node content",
	}
	testutils.MustExec(t, db.Save(&node), "preparing node")

	t.Run("owner accessing node", func(t *testing.T) {
		result := ViewNode(&user, node)
		assert.Equal(t, result, true, "result mismatch")
	})

	t.Run("non-owner accessing node", func(t *testing.T) {
		result := ViewNode(&anotherUser, node)
		assert.Equal(t, result, false, "result mismatch")
	})

	t.Run("guest accessing node", func(t *testing.T) {
		result := ViewNode(nil, node)
		assert.Equal(t, result, false, "result mismatch")
	})
}
