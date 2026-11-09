package batch

import (
	"context"
	"errors"
	"time"
	"web_backend/internal/models"

	"gorm.io/gorm"
)

type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Create(ctx context.Context, b *models.MigrationBatch, items []models.MigrationBatchItem) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(b).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].BatchID = b.ID
		}
		if len(items) == 0 {
			return nil
		}
		return tx.CreateInBatches(items, 100).Error
	})
}

func (s *Store) Get(ctx context.Context, batchID string) (*models.MigrationBatch, error) {
	var b models.MigrationBatch
	if err := s.db.WithContext(ctx).First(&b, "id = ?", batchID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBatchNotFound
		}
		return nil, err
	}
	var retries int64
	if err := s.db.WithContext(ctx).Model(&models.MigrationBatchItem{}).
		Where("batch_id = ? AND attempt > 1", b.ID).Count(&retries).Error; err != nil {
		return nil, err
	}
	b.ExecutionCount = int(retries)
	return &b, nil
}

func (s *Store) List(ctx context.Context, connectionID string, limit int) ([]models.MigrationBatch, error) {
	var batches []models.MigrationBatch
	if err := s.db.WithContext(ctx).
		Where("connection_id = ?", connectionID).
		Order("created_at DESC").Limit(limit).Find(&batches).Error; err != nil {
		return nil, err
	}
	if len(batches) == 0 {
		return batches, nil
	}
	ids := make([]string, 0, len(batches))
	for _, b := range batches {
		ids = append(ids, b.ID)
	}
	var counts []struct {
		BatchID string
		Total   int
	}
	if err := s.db.WithContext(ctx).Table("migration_batch_items").
		Select("batch_id::text AS batch_id, COUNT(*) AS total").
		Where("batch_id::text IN ? AND attempt > 1", ids).
		Group("batch_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]int, len(counts))
	for _, c := range counts {
		byID[c.BatchID] = c.Total
	}
	for i := range batches {
		batches[i].ExecutionCount = byID[batches[i].ID]
	}
	return batches, nil
}

func (s *Store) Items(ctx context.Context, batchID string, afterID int64, limit int) ([]models.MigrationBatchItem, error) {
	var items []models.MigrationBatchItem
	err := s.db.WithContext(ctx).Table("migration_batch_items AS i").
		Select("i.*, m.name AS migration_name").
		Joins("JOIN migrations m ON m.id = i.migration_id").
		Where("i.batch_id = ? AND i.id > ?", batchID, afterID).
		Order("i.id").Limit(limit).Scan(&items).Error
	return items, err
}

// Executions returns the append-only audit trail scoped to one connection.
func (s *Store) Executions(ctx context.Context, connectionID, batchID, migrationID string, afterID int64, limit int) ([]models.MigrationBatchItem, error) {
	q := s.db.WithContext(ctx).Table("migration_batch_items AS i").
		Select("i.*, m.name AS migration_name, m.sql AS migration_sql").
		Joins("JOIN migrations m ON m.id = i.migration_id").
		Joins("JOIN migration_batches b ON b.id = i.batch_id").
		Where("b.connection_id = ? AND i.id > ?", connectionID, afterID)
	if batchID != "" {
		q = q.Where("i.batch_id = ?", batchID)
	}
	if migrationID != "" {
		q = q.Where("i.migration_id = ?", migrationID)
	}
	var items []models.MigrationBatchItem
	err := q.Order("i.id").Limit(limit).Scan(&items).Error
	return items, err
}

// Pending returns the next page of not-yet-attempted items using the cursor so
// the cost stays flat regardless of batch size.
func (s *Store) Pending(ctx context.Context, batchID string, afterID int64, limit int) ([]models.MigrationBatchItem, error) {
	var items []models.MigrationBatchItem
	err := s.db.WithContext(ctx).Table("migration_batch_items AS i").
		Select("i.*, m.name AS migration_name").
		Joins("JOIN migrations m ON m.id = i.migration_id").
		Where("i.batch_id = ? AND i.status = ? AND i.id > ?", batchID, models.BatchItemPending, afterID).
		Order("i.id").Limit(limit).Scan(&items).Error
	return items, err
}

func (s *Store) RetryableItems(ctx context.Context, batchID string) ([]models.MigrationBatchItem, error) {
	var items []models.MigrationBatchItem
	err := s.db.WithContext(ctx).
		Where("batch_id = ? AND status IN ?", batchID, []models.BatchItemStatus{models.BatchItemPending, models.BatchItemSkipped}).
		Order("id").Find(&items).Error
	return items, err
}

func (s *Store) MaxAttempts(ctx context.Context, migrationIDs []string) (map[string]int, error) {
	var rows []struct {
		MigrationID string
		Attempt     int
	}
	if err := s.db.WithContext(ctx).Table("migration_batch_items").
		Select("migration_id::text AS migration_id, MAX(attempt) AS attempt").
		Where("migration_id::text IN ?", migrationIDs).
		Group("migration_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	attempts := make(map[string]int, len(rows))
	for _, row := range rows {
		attempts[row.MigrationID] = row.Attempt
	}
	return attempts, nil
}

func (s *Store) Drafts(ctx context.Context, connectionID string, migrationIDs []string) ([]models.Migration, error) {
	var drafts []models.Migration
	err := s.db.WithContext(ctx).
		Where("connection_id = ? AND status = ? AND id::text IN ?", connectionID, models.MigrationDraft, migrationIDs).
		Find(&drafts).Error
	return drafts, err
}

func (s *Store) MarkRunning(ctx context.Context, batchID string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&models.MigrationBatch{}).Where("id = ?", batchID).
		Updates(map[string]any{
			"status":       models.BatchRunning,
			"started_at":   now,
			"heartbeat_at": now,
		}).Error
}

func (s *Store) StartItem(ctx context.Context, itemID int64, now time.Time) error {
	return s.db.WithContext(ctx).Model(&models.MigrationBatchItem{}).Where("id = ?", itemID).
		Updates(map[string]any{
			"status":     models.BatchItemRunning,
			"started_at": now,
		}).Error
}

func (s *Store) FinishItem(ctx context.Context, itemID int64, status models.BatchItemStatus, errMsg string, durationMs int64, now time.Time) error {
	return s.db.WithContext(ctx).Model(&models.MigrationBatchItem{}).Where("id = ?", itemID).
		Updates(map[string]any{
			"status":      status,
			"error":       errMsg,
			"duration_ms": durationMs,
			"finished_at": now,
		}).Error
}

func (s *Store) Advance(ctx context.Context, batchID string, itemID int64, processedDelta, failedDelta int, now time.Time) error {
	return s.db.WithContext(ctx).Model(&models.MigrationBatch{}).Where("id = ?", batchID).
		Updates(map[string]any{
			"processed":    gorm.Expr("processed + ?", processedDelta),
			"failed":       gorm.Expr("failed + ?", failedDelta),
			"last_id":      itemID,
			"heartbeat_at": now,
		}).Error
}

func (s *Store) Heartbeat(ctx context.Context, batchID string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&models.MigrationBatch{}).Where("id = ?", batchID).
		Update("heartbeat_at", now).Error
}

func (s *Store) CancelState(ctx context.Context, batchID string) (bool, models.BatchStatus, error) {
	var row struct {
		CancelRequested bool
		Status          models.BatchStatus
	}
	if err := s.db.WithContext(ctx).Model(&models.MigrationBatch{}).
		Select("cancel_requested, status").Where("id = ?", batchID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, "", ErrBatchNotFound
		}
		return false, "", err
	}
	return row.CancelRequested, row.Status, nil
}

func (s *Store) RequestCancel(ctx context.Context, batchID, userID string) error {
	res := s.db.WithContext(ctx).Model(&models.MigrationBatch{}).
		Where("id = ? AND status IN ?", batchID, []models.BatchStatus{models.BatchQueued, models.BatchRunning}).
		Updates(map[string]any{
			"cancel_requested": true,
			"cancelled_by":     userID,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrBatchTerminal
	}
	return nil
}

// RetainCompensation records that applied migrations are kept on disk. The
// batch engine never reverts SQL; rollback belongs to the migration workflow.
func (s *Store) RetainCompensation(ctx context.Context, batchID string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&models.MigrationBatchItem{}).
		Where("batch_id = ? AND status = ? AND compensation_status = ?", batchID, models.BatchItemSuccess, models.CompensationNone).
		Updates(map[string]any{
			"compensation_status": models.CompensationRetained,
			"compensated_at":      now,
		}).Error
}

func (s *Store) Finish(ctx context.Context, batchID string, status models.BatchStatus, errMsg string, now time.Time) error {
	updates := map[string]any{
		"status":       status,
		"finished_at":  now,
		"heartbeat_at": now,
	}
	if errMsg != "" {
		updates["error"] = errMsg
	}
	return s.db.WithContext(ctx).Model(&models.MigrationBatch{}).Where("id = ?", batchID).
		Updates(updates).Error
}

// Stale returns batches whose owner stopped heartbeating, plus queued batches
// that never started (the process died between insert and run).
func (s *Store) Stale(ctx context.Context, cutoff time.Time) ([]models.MigrationBatch, error) {
	var batches []models.MigrationBatch
	err := s.db.WithContext(ctx).
		Where("(status = ? AND (heartbeat_at < ? OR (heartbeat_at IS NULL AND started_at < ?))) OR (status = ? AND created_at < ?)",
			models.BatchRunning, cutoff, cutoff, models.BatchQueued, cutoff).
		Find(&batches).Error
	return batches, err
}

func (s *Store) Recover(ctx context.Context, b *models.MigrationBatch, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MigrationBatchItem{}).
			Where("batch_id = ? AND status = ?", b.ID, models.BatchItemRunning).
			Updates(map[string]any{
				"status":      models.BatchItemFailed,
				"error":       "interrupted",
				"finished_at": now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.MigrationBatchItem{}).
			Where("batch_id = ? AND status = ? AND compensation_status = ?", b.ID, models.BatchItemSuccess, models.CompensationNone).
			Updates(map[string]any{
				"compensation_status": models.CompensationRetained,
				"compensated_at":      now,
			}).Error; err != nil {
			return err
		}
		status := models.BatchFailed
		if b.Processed > 0 {
			status = models.BatchPartial
		}
		return tx.Model(&models.MigrationBatch{}).Where("id = ?", b.ID).
			Updates(map[string]any{
				"status":       status,
				"error":        "interrupted",
				"finished_at":  now,
				"heartbeat_at": now,
			}).Error
	})
}
