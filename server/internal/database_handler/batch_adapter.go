package databasehandler

import (
	"context"
	"errors"

	"web_backend/internal/batch"
	"web_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// batchExecutor menghubungkan batch Coordinator ke execution engine bersama
type batchExecutor struct {
	h *Handler
}

func (h *Handler) NewBatchCoordinator() *batch.Coordinator {
	return batch.New(h.db, &batchExecutor{h: h})
}

func (e *batchExecutor) Acquire(ctx context.Context, connectionID string) (func(), error) {
	release, ok, err := e.h.engine.Locker().TryLock(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, batch.ErrBusy
	}
	return release, nil
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
	attempt, err := e.nextAttempt(ctx, executionID.String())
	if err != nil {
		return batch.Outcome{}, err
	}
	outcome, err := e.h.engine.RunLocked(ctx, connectionID, migrationID, executedBy, executionID.String(), attempt)
	if err != nil {
		return batch.Outcome{}, err
	}
	status := models.MigrationSuccess
	if outcome.Status == models.ExecutionFailed {
		status = models.MigrationFailed
	}
	return batch.Outcome{Status: status, DurationMs: outcome.DurationMs, Error: outcome.Error}, nil
}

// nextAttempt menjaga nomor attempt unik per execution ID di riwayat
func (e *batchExecutor) nextAttempt(ctx context.Context, executionID string) (int, error) {
	var count int64
	err := e.h.db.WithContext(ctx).Model(&models.MigrationExecution{}).
		Where("execution_id = ?", executionID).Count(&count).Error
	return int(count) + 1, err
}

var _ batch.Executor = (*batchExecutor)(nil)
