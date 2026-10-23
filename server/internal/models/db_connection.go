package models

import (
	"time"

	"gorm.io/gorm"
)

type DBConnection struct {
	ID          string         `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name        string         `json:"name" gorm:"column:name;type:varchar(100);not null;index:idx_db_connections_name,unique,where:deleted_at IS NULL"`
	Host        string         `json:"host" gorm:"column:host;type:varchar(255);not null"`
	Port        int            `json:"port" gorm:"column:port;not null;default:5432"`
	DBName      string         `json:"db_name" gorm:"column:db_name;type:varchar(100);not null"`
	Username    string         `json:"username" gorm:"column:username;type:varchar(100);not null"`
	PasswordEnc []byte         `json:"-" gorm:"column:password_enc;type:bytea;not null"`
	SSLMode     string         `json:"ssl_mode" gorm:"column:ssl_mode;type:varchar(20);not null;default:'prefer'"`
	CreatedBy   string         `json:"created_by" gorm:"column:created_by;type:varchar(64)"`
	CreatedAt   time.Time      `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time      `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

func (DBConnection) TableName() string { return "db_connections" }
