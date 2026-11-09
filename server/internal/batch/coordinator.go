package batch

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
	"web_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	defaultPageSize   = 100
	defaultStaleAfter = 60 * time.Second
	maxBatchSize      = 200
)

// Coordinator owns batch lifecycle: creation, sequential execution with
// per-item checkpoints, cancellation, retry and crash recovery. SQL execution
// is delegated to an Executor so the existing engine stays the only writer.
type Coordinator struct {
	store      *Store
	exec       Executor
	pageSize   int
	staleAfter time.Duration
	now        func() time.Time
}

func New(db *gorm.DB, exec Executor) *Coordinator {
	return &Coordinator{
		store:      NewStore(db),
		exec:       exec,
		pageSize:   defaultPageSize,
		staleAfter: defaultStaleAfter,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func (c *Coordinator) Create(ctx context.Context, connectionID, createdBy string, migrationIDs []string) (*models.MigrationBatch, error) {
	ids := normalizeIDs(migrationIDs)
	if len(ids) == 0 {
		return nil, ErrNoMigrations
	}
	if len(ids) > maxBatchSize {
		return nil, ErrTooManyMigrations
	}
	drafts, err := c.store.Drafts(ctx, connectionID, ids)
	if err != nil {
		return nil, err
	}
	if len(drafts) != len(ids) {
		return nil, ErrNotDraft
	}
	attempts, err := c.store.MaxAttempts(ctx, ids)
	if err != nil {
		return nil, err
	}
	b := &models.MigrationBatch{
		ConnectionID: connectionID,
		Status:       models.BatchQueued,
		Total:        len(ids),
		CreatedBy:    createdBy,
	}
	items := make([]models.MigrationBatchItem, len(ids))
	for i, id := range ids {
		items[i] = models.MigrationBatchItem{
			MigrationID: id,
			Attempt:     attempts[id] + 1,
			ExecutionID: uuid.NewString(),
			Status:      models.BatchItemPending,
		}
	}
	if err := c.store.Create(ctx, b, items); err != nil {
		return nil, err
	}
	return b, nil
}

// Start acquires the connection lock synchronously so a conflicting manual run
// is rejected with a 409 instead of failing after the batch was accepted.
func (c *Coordinator) Start(b *models.MigrationBatch) error {
	ctx := context.Background()
	release, err := c.exec.Acquire(ctx, b.ConnectionID)
	if err != nil {
		_ = c.store.Finish(ctx, b.ID, models.BatchFailed, err.Error(), c.now())
		return err
	}
	go c.run(ctx, b, release)
	return nil
}

func (c *Coordinator) run(ctx context.Context, b *models.MigrationBatch, release func()) {
	defer release()
	if err := c.store.MarkRunning(ctx, b.ID, c.now()); err != nil {
		log.Printf("[batch] mark running %s: %v", b.ID, err)
		return
	}

	var cursor int64
	processed, failed := 0, 0
	for {
		if cancelled, err := c.cancelled(ctx, b.ID); err != nil {
			c.finish(ctx, b, processed, failed, false, err.Error())
			return
		} else if cancelled {
			c.finish(ctx, b, processed, failed, true, "")
			return
		}

		page, err := c.store.Pending(ctx, b.ID, cursor, c.pageSize)
		if err != nil {
			c.finish(ctx, b, processed, failed, false, err.Error())
			return
		}
		if len(page) == 0 {
			break
		}

		for i := range page {
			item := page[i]
			if cancelled, err := c.cancelled(ctx, b.ID); err != nil {
				c.finish(ctx, b, processed, failed, false, err.Error())
				return
			} else if cancelled {
				c.finish(ctx, b, processed, failed, true, "")
				return
			}
			status, err := c.runItem(ctx, b, &item)
			switch {
			case errors.Is(err, ErrMigrationGone):
				_ = c.store.FinishItem(ctx, item.ID, models.BatchItemSkipped, err.Error(), 0, c.now())
				_ = c.store.Advance(ctx, b.ID, item.ID, 0, 0, c.now())
			case err != nil:
				_ = c.store.FinishItem(ctx, item.ID, models.BatchItemFailed, err.Error(), 0, c.now())
				_ = c.store.Advance(ctx, b.ID, item.ID, 0, 1, c.now())
				failed++
				c.finish(ctx, b, processed, failed, false, err.Error())
				return
			case status == models.BatchItemFailed:
				_ = c.store.Advance(ctx, b.ID, item.ID, 0, 1, c.now())
				failed++
			default:
				_ = c.store.Advance(ctx, b.ID, item.ID, 1, 0, c.now())
				processed++
			}
			cursor = item.ID
		}
	}
	c.finish(ctx, b, processed, failed, false, "")
}

// runItem executes one item and records its terminal state. SQL failures are
// returned as a failed item status; Go errors mean the run must stop.
func (c *Coordinator) runItem(ctx context.Context, b *models.MigrationBatch, item *models.MigrationBatchItem) (models.BatchItemStatus, error) {
	if err := c.store.StartItem(ctx, item.ID, c.now()); err != nil {
		return models.BatchItemFailed, err
	}
	executionID, err := uuid.Parse(item.ExecutionID)
	if err != nil {
		return models.BatchItemFailed, err
	}
	outcome, err := c.exec.ExecuteLocked(ctx, b.ConnectionID, item.MigrationID, b.CreatedBy, executionID)
	if err != nil {
		return models.BatchItemFailed, err
	}
	status := models.BatchItemSuccess
	if outcome.Status == models.MigrationFailed {
		status = models.BatchItemFailed
	}
	if err := c.store.FinishItem(ctx, item.ID, status, outcome.Error, outcome.DurationMs, c.now()); err != nil {
		return models.BatchItemFailed, err
	}
	return status, nil
}

func (c *Coordinator) finish(ctx context.Context, b *models.MigrationBatch, processed, failed int, cancelled bool, errMsg string) {
	now := c.now()
	status := models.BatchSuccess
	switch {
	case cancelled:
		status = models.BatchCancelled
	case failed > 0 && processed > 0:
		status = models.BatchPartial
	case failed > 0:
		status = models.BatchFailed
	}
	if err := c.store.RetainCompensation(ctx, b.ID, now); err != nil {
		log.Printf("[batch] retain compensation %s: %v", b.ID, err)
	}
	if err := c.store.Finish(ctx, b.ID, status, errMsg, now); err != nil {
		log.Printf("[batch] finish %s: %v", b.ID, err)
	}
}

func (c *Coordinator) cancelled(ctx context.Context, batchID string) (bool, error) {
	requested, status, err := c.store.CancelState(ctx, batchID)
	if err != nil {
		return false, err
	}
	if status != models.BatchRunning {
		return true, nil
	}
	return requested, nil
}

func (c *Coordinator) Cancel(ctx context.Context, batchID, userID string) error {
	_, status, err := c.store.CancelState(ctx, batchID)
	if err != nil {
		return err
	}
	if status != models.BatchQueued && status != models.BatchRunning {
		return ErrBatchTerminal
	}
	return c.store.RequestCancel(ctx, batchID, userID)
}

// Retry appends a new batch with fresh audit rows for the items that never got
// an outcome. Failed migrations are intentionally not retried because the
// migration contract forbids re-running them.
func (c *Coordinator) Retry(ctx context.Context, b *models.MigrationBatch, userID string) (*models.MigrationBatch, error) {
	items, err := c.store.RetryableItems(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.MigrationID)
	}
	if len(ids) == 0 {
		return nil, ErrNothingToRetry
	}
	return c.Create(ctx, b.ConnectionID, userID, ids)
}

// Recover closes batches whose runner died (heartbeat older than staleAfter)
// and marks in-flight items as interrupted so progress is never left hanging.
func (c *Coordinator) Recover(ctx context.Context) (int, error) {
	cutoff := c.now().Add(-c.staleAfter)
	batches, err := c.store.Stale(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	recovered := 0
	for i := range batches {
		if err := c.store.Recover(ctx, &batches[i], c.now()); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

func normalizeIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// Store exposes read helpers for HTTP list/detail handlers.
func (c *Coordinator) Store() *Store { return c.store }
