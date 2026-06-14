package app

import (
	"time"

	"github.com/lflow/lflow/pkg/server/database"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// incrementUserUSN increment the given user's max_usn by 1
// and returns the new, incremented max_usn
func incrementUserUSN(tx *gorm.DB, userID int) (int, error) {
	// First, get the current max_usn to detect transition from empty server
	var user database.User
	if err := tx.Select("max_usn, full_sync_before").Where("id = ?", userID).First(&user).Error; err != nil {
		return 0, errors.Wrap(err, "getting current user state")
	}

	// If transitioning from empty server (MaxUSN=0) to non-empty (MaxUSN=1),
	// set full_sync_before to current timestamp to force all other clients to full sync
	if user.MaxUSN == 0 && user.FullSyncBefore == 0 {
		currentTime := time.Now().Unix()
		if err := tx.Table("users").Where("id = ?", userID).Update("full_sync_before", currentTime).Error; err != nil {
			return 0, errors.Wrap(err, "setting full_sync_before on empty server transition")
		}
	}

	if err := tx.Table("users").Where("id = ?", userID).Update("max_usn", gorm.Expr("max_usn + 1")).Error; err != nil {
		return 0, errors.Wrap(err, "incrementing user max_usn")
	}

	if err := tx.Select("max_usn").Where("id = ?", userID).First(&user).Error; err != nil {
		return 0, errors.Wrap(err, "getting the updated user max_usn")
	}

	return user.MaxUSN, nil
}
