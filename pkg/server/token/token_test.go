package token

import (
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/server/testutils"
	"github.com/lflow/lflow/pkg/shared/assert"
	"github.com/pkg/errors"
)

func TestCreate(t *testing.T) {
	testCases := []struct {
		kind string
	}{
		{
			kind: database.TokenTypeResetPassword,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("token type %s", tc.kind), func(t *testing.T) {
			db := testutils.InitMemoryDB(t)

			// Set up
			u := testutils.SetupUserData(db, "user@test.com", "password123")

			// Execute
			tok, err := Create(db, u.ID, tc.kind)
			if err != nil {
				t.Fatal(errors.Wrap(err, "performing"))
			}

			// Test
			var count int64
			testutils.MustExec(t, db.Model(&database.Token{}).Count(&count), "counting token")
			assert.Equalf(t, count, int64(1), "error mismatch")

			var tokenRecord database.Token
			testutils.MustExec(t, db.First(&tokenRecord), "finding token")
			assert.Equalf(t, tokenRecord.UserID, tok.UserID, "UserID mismatch")
			assert.Equalf(t, tokenRecord.Value, tok.Value, "Value mismatch")
			assert.Equalf(t, tokenRecord.Type, tok.Type, "Type mismatch")
		})
	}
}
