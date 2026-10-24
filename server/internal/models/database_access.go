package models

import (
	"time"
)

type AccessLevel string

const (
	AccessNone AccessLevel = ""
	AccessView AccessLevel = "view"
	AccessEdit AccessLevel = "edit"
)

type GroupDatabaseAccess struct {
	GroupID      string      `json:"group_id" gorm:"column:group_id;type:uuid;primaryKey"`
	ConnectionID string      `json:"connection_id" gorm:"column:connection_id;type:uuid;primaryKey;index"`
	Level        AccessLevel `json:"level" gorm:"column:level;type:varchar(10);not null;check:chk_group_database_access_level,level IN ('view','edit')"`
	CreatedAt    time.Time   `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt    time.Time   `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`

	Group      Group        `json:"-" gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`
	Connection DBConnection `json:"-" gorm:"foreignKey:ConnectionID;constraint:OnDelete:CASCADE"`
}

func (GroupDatabaseAccess) TableName() string { return "group_database_access" }
