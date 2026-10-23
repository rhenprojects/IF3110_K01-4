package models

import (
	"time"
)

type MigrationStatus string

const (
	MigrationDraft   MigrationStatus = "draft"
	MigrationSuccess MigrationStatus = "success"
	MigrationFailed  MigrationStatus = "failed"
)

// Migration adalah satu perubahan SQL pada database target. Version diisi saat
// migration berhasil dieksekusi, sehingga riwayat tiap database linear (1, 2, 3, ...).
type Migration struct {
	ID           string          `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ConnectionID string          `json:"connection_id" gorm:"column:connection_id;type:uuid;not null;index;index:idx_migrations_connection_version,unique,where:version IS NOT NULL"`
	Version      *int            `json:"version" gorm:"column:version;index:idx_migrations_connection_version,unique,where:version IS NOT NULL"`
	Name         string          `json:"name" gorm:"column:name;type:varchar(200);not null"`
	SQL          string          `json:"sql" gorm:"column:sql;type:text;not null"`
	Status       MigrationStatus `json:"status" gorm:"column:status;type:varchar(10);not null;default:'draft';check:chk_migrations_status,status IN ('draft','success','failed')"`
	Error        string          `json:"error,omitempty" gorm:"column:error;type:text;not null;default:''"`
	CreatedBy    string          `json:"created_by" gorm:"column:created_by;type:varchar(64);not null"`
	ExecutedBy   *string         `json:"executed_by" gorm:"column:executed_by;type:varchar(64)"`
	CreatedAt    time.Time       `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt    time.Time       `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	ExecutedAt   *time.Time      `json:"executed_at" gorm:"column:executed_at;type:timestamptz"`
	DurationMs   *int64          `json:"duration_ms" gorm:"column:duration_ms"`

	Connection DBConnection `json:"-" gorm:"foreignKey:ConnectionID;constraint:OnDelete:RESTRICT"`
}

func (Migration) TableName() string { return "migrations" }
