package app

import (
	"time"

	"github.com/lflow/lflow/pkg/server/crypt"
	"github.com/lflow/lflow/pkg/server/database"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// CreateSession returns a new session for the user of the given id
func (a *App) CreateSession(userID int) (database.Session, error) {
	key, err := crypt.GetRandomStr(32)
	if err != nil {
		return database.Session{}, errors.Wrap(err, "generating key")
	}

	session := database.Session{
		UserID:     userID,
		Key:        key,
		LastUsedAt: time.Now(),
		ExpiresAt:  time.Now().Add(24 * 100 * time.Hour),
	}

	if err := a.DB.Save(&session).Error; err != nil {
		return database.Session{}, errors.Wrap(err, "saving session")
	}

	return session, nil
}

// DeleteUserSessions deletes all existing sessions for the given user. It effectively
// invalidates all existing sessions.
func (a *App) DeleteUserSessions(db *gorm.DB, userID int) error {
	if err := db.Where("user_id = ?", userID).Delete(&database.Session{}).Error; err != nil {
		return errors.Wrap(err, "deleting sessions")
	}

	return nil
}

// DeleteSession deletes the session that match the given info
func (a *App) DeleteSession(sessionKey string) error {
	if err := a.DB.Where("key = ?", sessionKey).Delete(&database.Session{}).Error; err != nil {
		return errors.Wrap(err, "deleting the session")
	}

	return nil
}
