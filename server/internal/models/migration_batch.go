package models

import "time"

type BatchStatus string

const (
	BatchQueued    BatchStatus = "queued"
	BatchRunning   BatchStatus = "running"
	BatchSuccess   BatchStatus = "success"
	BatchPartial   BatchStatus = "partial"
	BatchFailed    BatchStatus = "failed"
	BatchCancelled BatchStatus = "cancelled"
)

// MigrationBatch tracks one run over a user-selected set of draft migrations.
// LastID is the pagination cursor over migration_batch_items.id so progress
// survives restarts without OFFSET scans.
type MigrationBatch struct {
	ID              string      `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ConnectionID    string      `json:"connection_id" gorm:"column:connection_id;type:uuid;not null;index:idx_migration_batches_connection_created,priority:1"`
	Status          BatchStatus `json:"status" gorm:"column:status;type:varchar(10);not null;default:'queued';check:chk_migration_batches_status,status IN ('queued','running','success','partial','failed','cancelled')"`
	Total           int         `json:"total" gorm:"column:total;not null;default:0"`
	Processed       int         `json:"processed" gorm:"column:processed;not null;default:0"`
	Failed          int         `json:"failed" gorm:"column:failed;not null;default:0"`
	LastID          int64       `json:"-" gorm:"column:last_id;not null;default:0"`
	CancelRequested bool        `json:"cancel_requested" gorm:"column:cancel_requested;not null;default:false"`
	CreatedBy       string      `json:"created_by" gorm:"column:created_by;type:varchar(64);not null"`
	CancelledBy     *string     `json:"cancelled_by" gorm:"column:cancelled_by;type:varchar(64)"`
	Error           string      `json:"error" gorm:"column:error;type:text;not null;default:''"`
	HeartbeatAt     *time.Time  `json:"heartbeat_at" gorm:"column:heartbeat_at;type:timestamptz"`
	StartedAt       *time.Time  `json:"started_at" gorm:"column:started_at;type:timestamptz"`
	FinishedAt      *time.Time  `json:"finished_at" gorm:"column:finished_at;type:timestamptz"`
	CreatedAt       time.Time   `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP;index:idx_migration_batches_connection_created,priority:2,sort:desc"`
	UpdatedAt       time.Time   `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`

	ExecutionCount  int     `json:"execution_count" gorm:"-"`
	CreatedByName   string  `json:"created_by_name,omitempty" gorm:"-"`
	CancelledByName *string `json:"cancelled_by_name,omitempty" gorm:"-"`

	Connection DBConnection `json:"-" gorm:"foreignKey:ConnectionID;constraint:OnDelete:RESTRICT"`
}

func (MigrationBatch) TableName() string { return "migration_batches" }

type BatchItemStatus string

const (
	BatchItemPending BatchItemStatus = "pending"
	BatchItemRunning BatchItemStatus = "running"
	BatchItemSuccess BatchItemStatus = "success"
	BatchItemFailed  BatchItemStatus = "failed"
	BatchItemSkipped BatchItemStatus = "skipped"
)

type CompensationStatus string

const (
	CompensationNone       CompensationStatus = "none"
	CompensationRetained   CompensationStatus = "retained"
	CompensationRolledBack CompensationStatus = "rolled_back"
)

// MigrationBatchItem is the immutable audit trail of one attempt to execute one
// migration inside a batch. Retrying a batch appends new rows instead of
// rewriting history.
type MigrationBatchItem struct {
	ID                 int64              `json:"id" gorm:"type:bigint;primaryKey;autoIncrement"`
	BatchID            string             `json:"batch_id" gorm:"column:batch_id;type:uuid;not null;uniqueIndex:idx_batch_items_batch_migration,priority:1;index:idx_batch_items_batch_id,priority:1"`
	MigrationID        string             `json:"migration_id" gorm:"column:migration_id;type:uuid;not null;uniqueIndex:idx_batch_items_batch_migration,priority:2;index:idx_batch_items_migration"`
	Attempt            int                `json:"attempt" gorm:"column:attempt;not null;default:1"`
	ExecutionID        string             `json:"execution_id" gorm:"column:execution_id;type:uuid;not null;default:gen_random_uuid()"`
	Status             BatchItemStatus    `json:"status" gorm:"column:status;type:varchar(10);not null;default:'pending';check:chk_migration_batch_items_status,status IN ('pending','running','success','failed','skipped')"`
	Error              string             `json:"error" gorm:"column:error;type:text;not null;default:''"`
	CompensationStatus CompensationStatus `json:"compensation_status" gorm:"column:compensation_status;type:varchar(12);not null;default:'none';check:chk_migration_batch_items_compensation,compensation_status IN ('none','retained','rolled_back')"`
	CompensatedAt      *time.Time         `json:"compensated_at" gorm:"column:compensated_at;type:timestamptz"`
	StartedAt          *time.Time         `json:"started_at" gorm:"column:started_at;type:timestamptz"`
	FinishedAt         *time.Time         `json:"finished_at" gorm:"column:finished_at;type:timestamptz"`
	DurationMs         *int64             `json:"duration_ms" gorm:"column:duration_ms;type:bigint"`
	CreatedAt          time.Time          `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt          time.Time          `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`

	MigrationName string `json:"migration_name,omitempty" gorm:"column:migration_name;->;-:migration"`
	MigrationSQL  string `json:"-" gorm:"column:migration_sql;->;-:migration"`

	Batch     MigrationBatch `json:"-" gorm:"foreignKey:BatchID;constraint:OnDelete:CASCADE"`
	Migration *Migration     `json:"-" gorm:"foreignKey:MigrationID;constraint:OnDelete:RESTRICT"`
}

func (MigrationBatchItem) TableName() string { return "migration_batch_items" }
