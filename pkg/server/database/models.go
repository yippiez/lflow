package database

import (
	"time"
)

// Model is the base model definition
type Model struct {
	ID        int       `gorm:"primaryKey" json:"-"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// Node is a model for an outline node: every bullet, heading, todo and
// mirror instance is a node. ParentUUID == "" means a root node.
type Node struct {
	Model
	UUID        string `json:"uuid" gorm:"index;type:text"`
	User        User   `json:"user"`
	UserID      int    `json:"user_id" gorm:"index"`
	ParentUUID  string `json:"parent_uuid" gorm:"index;type:text"`
	Rank        int    `json:"rank"`
	Name        string `json:"name"`
	Note        string `json:"note"`
	Layout      string `json:"layout" gorm:"default:bullets"`
	MirrorOf    string `json:"mirror_of" gorm:"type:text"`
	CompletedAt int64  `json:"completed_at" gorm:"default:0"`
	AddedOn     int64  `json:"added_on"`
	EditedOn    int64  `json:"edited_on"`
	USN         int    `json:"-" gorm:"index"`
	Deleted     bool   `json:"-" gorm:"default:false"`
	Client      string `gorm:"index"`
}

// User is a model for a user
type User struct {
	Model
	UUID           string     `json:"uuid" gorm:"type:text;index"`
	Email          NullString `gorm:"index"`
	Password       NullString `json:"-"`
	LastLoginAt    *time.Time `json:"-"`
	MaxUSN         int        `json:"-" gorm:"default:0"`
	FullSyncBefore int64      `json:"-" gorm:"default:0"`
}

// Token is a model for a token
type Token struct {
	Model
	UserID int    `gorm:"index"`
	Value  string `gorm:"index"`
	Type   string
	UsedAt *time.Time
}

// Session represents a user session
type Session struct {
	Model
	UserID     int    `gorm:"index"`
	Key        string `gorm:"index"`
	LastUsedAt time.Time
	ExpiresAt  time.Time
}
