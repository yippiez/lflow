package session

import (
	"github.com/lflow/lflow/pkg/server/database"
)

// Session represents user session
type Session struct {
	UUID  string `json:"uuid"`
	Email string `json:"email"`
}

// New returns a new session for the given user
func New(user database.User) Session {
	return Session{
		UUID:  user.UUID,
		Email: user.Email.String,
	}
}
