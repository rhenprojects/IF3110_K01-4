package models

import "time"

// AuditLog mencatat mutasi API dan percobaan login. Tidak menyimpan password
// atau body request.
type AuditLog struct {
	ID         int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	ActorID    string    `json:"actor_id" gorm:"column:actor_id;type:varchar(64);not null;default:'';index"`
	Action     string    `json:"action" gorm:"column:action;type:varchar(100);not null"`
	TargetType string    `json:"target_type" gorm:"column:target_type;type:varchar(50);not null;default:''"`
	TargetID   string    `json:"target_id" gorm:"column:target_id;type:varchar(200);not null;default:''"`
	IP         string    `json:"ip" gorm:"column:ip;type:varchar(100);not null;default:''"`
	Detail     string    `json:"detail" gorm:"column:detail;type:text;not null;default:''"`
	CreatedAt  time.Time `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP;index"`
}

func (AuditLog) TableName() string { return "audit_logs" }
