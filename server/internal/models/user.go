package models

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID        string         `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Username  string         `json:"username" gorm:"column:username;type:varchar(50);uniqueIndex;not null"`
	Name      string         `json:"name" gorm:"column:name;type:varchar(100);not null"`
	Password  string         `json:"-" gorm:"column:password_hash;type:varchar(255);not null"`
	IsAdmin   bool           `json:"is_admin" gorm:"column:is_admin;not null;default:false"`
	CreatedAt time.Time      `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

func (User) TableName() string { return "users" }

type CreateUserRequest struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type UpdateUserRequest struct {
	Name     *string `json:"name"`
	Password *string `json:"password"`
	IsAdmin  *bool   `json:"is_admin"`
}
