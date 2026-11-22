package models

import "time"

type JobKind string

const (
	JobKindUp       JobKind = "up"
	JobKindRollback JobKind = "rollback"
	JobKindForward  JobKind = "forward"
)

type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobSuccess JobStatus = "success"
	JobFailed  JobStatus = "failed"
	JobUncertain JobStatus = "uncertain"
)

// MigrationJob adalah satu permintaan eksekusi (up, rollback, atau forward) di queue.
type MigrationJob struct {
	ID           string     `json:"id" gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ConnectionID string     `json:"connection_id" gorm:"column:connection_id;type:uuid;not null;index"`
	MigrationID  string     `json:"migration_id" gorm:"column:migration_id;type:uuid;not null;index;index:idx_migration_jobs_active,unique,where:status <> 'success' AND status <> 'failed'"`
	ExecutionID  string     `json:"execution_id" gorm:"column:execution_id;type:uuid;not null;uniqueIndex"`
	Kind         JobKind    `json:"kind" gorm:"column:kind;type:varchar(10);not null;check:chk_migration_jobs_kind,kind IN ('up','rollback','forward')"`
	Status       JobStatus  `json:"status" gorm:"column:status;type:varchar(10);not null;default:'queued';index:idx_migration_jobs_status_run_after,priority:1;check:chk_migration_jobs_status,status IN ('queued','running','success','failed','uncertain')"`
	RollbackOf   *string    `json:"rollback_of" gorm:"column:rollback_of;type:uuid"`
	Attempts     int        `json:"attempts" gorm:"column:attempts;not null;default:0"`
	MaxAttempts  int        `json:"max_attempts" gorm:"column:max_attempts;not null;default:3"`
	RunAfter     time.Time  `json:"run_after" gorm:"column:run_after;type:timestamptz;not null;default:CURRENT_TIMESTAMP;index:idx_migration_jobs_status_run_after,priority:2"`
	WorkerID     string     `json:"worker_id" gorm:"column:worker_id;type:varchar(100);not null;default:''"`
	HeartbeatAt  *time.Time `json:"heartbeat_at" gorm:"column:heartbeat_at;type:timestamptz"`
	TraceParent  string     `json:"-" gorm:"column:trace_parent;type:varchar(100);not null;default:''"`
	Error        string     `json:"error" gorm:"column:error;type:text;not null;default:''"`
	RequestedBy  string     `json:"requested_by" gorm:"column:requested_by;type:varchar(64);not null"`
	CreatedAt    time.Time  `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`
	StartedAt    *time.Time `json:"started_at" gorm:"column:started_at;type:timestamptz"`
	FinishedAt   *time.Time `json:"finished_at" gorm:"column:finished_at;type:timestamptz"`

	MigrationName string `json:"migration_name,omitempty" gorm:"-"`

	Connection DBConnection `json:"-" gorm:"foreignKey:ConnectionID;constraint:OnDelete:RESTRICT"`
	Migration  Migration    `json:"-" gorm:"foreignKey:MigrationID;constraint:OnDelete:RESTRICT"`
}

func (MigrationJob) TableName() string { return "migration_jobs" }

type ExecutionStatus string

const (
	ExecutionSuccess ExecutionStatus = "success"
	ExecutionFailed  ExecutionStatus = "failed"
)

// MigrationExecution adalah catatan riwayat satu attempt, berhasil maupun gagal.
type MigrationExecution struct {
	ID               int64           `json:"id" gorm:"type:bigint;primaryKey;autoIncrement"`
	ExecutionID      string          `json:"execution_id" gorm:"column:execution_id;type:uuid;not null;uniqueIndex:idx_migration_executions_attempt,priority:1"`
	Attempt          int             `json:"attempt" gorm:"column:attempt;not null;uniqueIndex:idx_migration_executions_attempt,priority:2"`
	JobID            *string         `json:"job_id" gorm:"column:job_id;type:uuid;index"`
	ConnectionID     string          `json:"connection_id" gorm:"column:connection_id;type:uuid;not null;index"`
	MigrationID      string          `json:"migration_id" gorm:"column:migration_id;type:uuid;not null;index"`
	Kind             JobKind         `json:"kind" gorm:"column:kind;type:varchar(10);not null"`
	Status           ExecutionStatus `json:"status" gorm:"column:status;type:varchar(10);not null;check:chk_migration_executions_status,status IN ('success','failed')"`
	Revision         int             `json:"revision" gorm:"column:revision;not null"`
	MigrationVersion *int            `json:"migration_version" gorm:"column:migration_version;type:bigint"`
	SQL              string          `json:"sql" gorm:"column:sql;type:text;not null"`
	RollbackOf       *string         `json:"rollback_of" gorm:"column:rollback_of;type:uuid"`
	ExecutedBy       string          `json:"executed_by" gorm:"column:executed_by;type:varchar(64);not null"`
	StartedAt        time.Time       `json:"started_at" gorm:"column:started_at;type:timestamptz;not null"`
	FinishedAt       time.Time       `json:"finished_at" gorm:"column:finished_at;type:timestamptz;not null"`
	DurationMs       int64           `json:"duration_ms" gorm:"column:duration_ms;type:bigint;not null"`
	Error            string          `json:"error" gorm:"column:error;type:text;not null;default:''"`
	SQLState         string          `json:"sqlstate" gorm:"column:sqlstate;type:varchar(5);not null;default:''"`
	Recovered        bool            `json:"recovered" gorm:"column:recovered;not null;default:false"`
	CreatedAt        time.Time       `json:"created_at" gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP"`

	Migration Migration `json:"-" gorm:"foreignKey:MigrationID;constraint:OnDelete:RESTRICT"`
}

func (MigrationExecution) TableName() string { return "migration_executions" }

type JobListResponse struct {
	Jobs []MigrationJob `json:"jobs"`
}

type ExecutionListResponse struct {
	Executions []MigrationExecution `json:"executions"`
}
