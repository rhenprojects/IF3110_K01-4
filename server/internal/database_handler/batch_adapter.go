package databasehandler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"web_backend/internal/batch"
	"web_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// batchExecutor adapts the existing single-migration engine to the batch
// Coordinator. TODO(orang-1): replace with the shared ExecuteOne entry point
// once the engine exposes it; delete this file after the merge.
type batchExecutor struct {
	h *Handler
}

func (h *Handler) NewBatchCoordinator() *batch.Coordinator {
	return batch.New(h.db, &batchExecutor{h: h})
}

func (e *batchExecutor) Acquire(ctx context.Context, connectionID string) (func(), error) {
	lock := e.h.lockFor(connectionID)
	if !lock.TryLock() {
		return nil, batch.ErrBusy
	}
	return lock.Unlock, nil
}

func (e *batchExecutor) ExecuteLocked(ctx context.Context, connectionID, migrationID, executedBy string, executionID uuid.UUID) (batch.Outcome, error) {
	if _, err := uuid.Parse(migrationID); err != nil {
		return batch.Outcome{}, batch.ErrMigrationGone
	}
	var migration models.Migration
	if err := e.h.db.WithContext(ctx).
		First(&migration, "id = ? AND connection_id = ?", migrationID, connectionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return batch.Outcome{}, batch.ErrMigrationGone
		}
		return batch.Outcome{}, err
	}
	if migration.Status != models.MigrationDraft {
		return batch.Outcome{}, batch.ErrMigrationGone
	}
	pool, err := e.h.svc.Pool(ctx, connectionID)
	if err != nil {
		return batch.Outcome{}, err
	}

	started := time.Now()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return batch.Outcome{}, err
	}
	_, execErr := tx.Exec(ctx, migration.SQL)
	duration := time.Since(started).Milliseconds()
	if duration < 1 {
		duration = 1
	}
	now := time.Now().UTC()
	migration.ExecutedBy = &executedBy
	migration.ExecutedAt = &now
	migration.DurationMs = &duration

	if execErr != nil {
		_ = tx.Rollback(ctx)
		migration.Status = models.MigrationFailed
		migration.Error = execErr.Error()
		if err := e.h.db.WithContext(ctx).Save(&migration).Error; err != nil {
			return batch.Outcome{}, err
		}
		return batch.Outcome{Status: models.MigrationFailed, DurationMs: duration, Error: execErr.Error()}, nil
	}

	version, err := e.h.nextVersion(ctx, connectionID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return batch.Outcome{}, err
	}
	versionPtr := toIntPtr(version)
	migration.Version = versionPtr
	migration.Status = models.MigrationSuccess
	migration.Error = ""
	if err := e.h.db.WithContext(ctx).Save(&migration).Error; err != nil {
		_ = tx.Rollback(ctx)
		return batch.Outcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		migration.Status = models.MigrationFailed
		migration.Error = fmt.Sprintf("commit failed: %v", err)
		if saveErr := e.h.db.WithContext(context.Background()).Save(&migration).Error; saveErr != nil {
			return batch.Outcome{}, saveErr
		}
		return batch.Outcome{}, fmt.Errorf("commit failed: %w", err)
	}
	return batch.Outcome{Status: models.MigrationSuccess, DurationMs: duration}, nil
}

func toIntPtr(v int) *int {
	return &v
}
