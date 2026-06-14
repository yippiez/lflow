package app

import (
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/assert"
	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/server/testutils"
	"github.com/pkg/errors"
)

func TestIncremenetUserUSN(t *testing.T) {
	testCases := []struct {
		maxUSN         int
		expectedMaxUSN int
	}{
		{
			maxUSN:         1,
			expectedMaxUSN: 2,
		},
		{
			maxUSN:         1988,
			expectedMaxUSN: 1989,
		},
	}

	// set up
	for idx, tc := range testCases {
		func() {
			db := testutils.InitMemoryDB(t)

			user := testutils.SetupUserData(db, "user@test.com", "password123")
			testutils.MustExec(t, db.Model(&user).Update("max_usn", tc.maxUSN), fmt.Sprintf("preparing user max_usn for test case %d", idx))

			// execute
			tx := db.Begin()
			nextUSN, err := incrementUserUSN(tx, user.ID)
			if err != nil {
				t.Fatal(errors.Wrap(err, "incrementing the user usn"))
			}
			tx.Commit()

			// test
			var userRecord database.User
			testutils.MustExec(t, db.Where("id = ?", user.ID).First(&userRecord), fmt.Sprintf("finding user for test case %d", idx))

			assert.Equal(t, userRecord.MaxUSN, tc.expectedMaxUSN, fmt.Sprintf("user max_usn mismatch for case %d", idx))
			assert.Equal(t, nextUSN, tc.expectedMaxUSN, fmt.Sprintf("next_usn mismatch for case %d", idx))
		}()
	}
}
