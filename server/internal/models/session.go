package models

import (
	"time"
)

type Session struct {
	ID                string     `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID            string     `json:"user_id" gorm:"column:user_id;type:uuid;not null;index"`
	TokenHash         []byte     `json:"-" gorm:"column:token_hash;type:bytea;not null;uniqueIndex"`
	PrevTokenHash     []byte     `json:"-" gorm:"column:prev_token_hash;type:bytea;index"`
	PrevValidUntil    *time.Time `json:"-" gorm:"column:prev_valid_until;type:timestamptz"`
	CSRFToken         string     `json:"-" gorm:"column:csrf_token;type:varchar(64);not null"`
	CreatedAt         time.Time  `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	LastSeenAt        time.Time  `json:"last_seen_at" gorm:"column:last_seen_at;type:timestamptz;not null"`
	RotatedAt         time.Time  `json:"rotated_at" gorm:"column:rotated_at;type:timestamptz;not null"`
	AbsoluteExpiresAt time.Time  `json:"absolute_expires_at" gorm:"column:absolute_expires_at;type:timestamptz;not null"`
	RevokedAt         *time.Time `json:"revoked_at" gorm:"column:revoked_at;type:timestamptz"`

	User User `json:"-" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

func (Session) TableName() string { return "sessions" }
