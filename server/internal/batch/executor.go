package batch

import (
	"context"
	"errors"
	"web_backend/internal/models"

	"github.com/google/uuid"
)

var (
	ErrBusy              = errors.New("another migration is already running on this database")
	ErrBatchNotFound     = errors.New("batch not found")
	ErrBatchTerminal     = errors.New("batch is already finished")
	ErrNoMigrations      = errors.New("migration_ids is required")
	ErrTooManyMigrations = errors.New("at most 200 migrations per batch")
	ErrNotDraft          = errors.New("only draft migrations can be executed")
	ErrMigrationGone     = errors.New("migration is no longer a draft")
	ErrNothingToRetry    = errors.New("batch has no pending or skipped items")
)

// Outcome is the result of one migration execution. SQL errors are reported
// here as a failed outcome instead of a Go error so the batch can keep going;
// Go errors are reserved for infrastructure failures that should stop the run.
type Outcome struct {
	Status     models.MigrationStatus
	DurationMs int64
	Error      string
}

// Executor abstracts the existing single-migration engine so this package does
// not own locking or SQL execution. Acquire must be held for the whole batch so
// a manual single execution cannot interleave with it.
type Executor interface {
	Acquire(ctx context.Context, connectionID string) (release func(), err error)
	ExecuteLocked(ctx context.Context, connectionID, migrationID, executedBy string, executionID uuid.UUID) (Outcome, error)
}
