package session

import (
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/shared/assert"
)

func TestNew(t *testing.T) {
	u1 := database.User{
		UUID:  "0f5f0054-d23f-4be1-b5fb-57673109e9cb",
		Email: database.ToNullString("alice@example.com"),
	}

	u2 := database.User{
		UUID:  "718a1041-bbe6-496e-bbe4-ea7e572c295e",
		Email: database.ToNullString("bob@example.com"),
	}

	testCases := []struct {
		user database.User
	}{
		{
			user: u1,
		},
		{
			user: u2,
		},
	}

	for idx, tc := range testCases {
		t.Run(fmt.Sprintf("user %d", idx), func(t *testing.T) {
			// Execute
			got := New(tc.user)
			expected := Session{
				UUID:  tc.user.UUID,
				Email: tc.user.Email.String,
			}

			assert.DeepEqual(t, got, expected, "result mismatch")
		})
	}
}
