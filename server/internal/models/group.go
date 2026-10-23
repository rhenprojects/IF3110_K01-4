package models

import (
	"time"
)

type Group struct {
	ID          string    `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name        string    `json:"name" gorm:"column:name;type:varchar(100);uniqueIndex;not null"`
	Description string    `json:"description" gorm:"column:description;type:varchar(255);not null;default:''"`
	CreatedAt   time.Time `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
}

func (Group) TableName() string { return "groups" }

type GroupMember struct {
	GroupID   string    `json:"group_id" gorm:"column:group_id;type:uuid;primaryKey"`
	UserID    string    `json:"user_id" gorm:"column:user_id;type:uuid;primaryKey;index"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`

	Group Group `json:"-" gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`
	User  User  `json:"-" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

func (GroupMember) TableName() string { return "group_members" }
