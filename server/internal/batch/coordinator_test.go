package batch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"web_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type fakeExecutor struct {
	fail map[string]bool
	seen []string
}

func (f *fakeExecutor) Acquire(ctx context.Context, connectionID string) (func(), error) {
	return func() {}, nil
}

func (f *fakeExecutor) ExecuteLocked(ctx context.Context, connectionID, migrationID, executedBy string, executionID uuid.UUID) (Outcome, error) {
	f.seen = append(f.seen, migrationID)
	if f.fail[migrationID] {
		return Outcome{Status: models.MigrationFailed, DurationMs: 1, Error: "boom"}, nil
	}
	return Outcome{Status: models.MigrationSuccess, DurationMs: 1}, nil
}

func TestNormalizeIDs(t *testing.T) {
	got := normalizeIDs([]string{" A ", "a", "", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("normalizeIDs = %v", got)
	}
}

func batchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&models.DBConnection{}, &models.Migration{}, &models.MigrationBatch{}, &models.MigrationBatchItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seed(t *testing.T, db *gorm.DB, drafts int, status models.MigrationStatus) (string, []string) {
	t.Helper()
	conn := models.DBConnection{
		Name: fmt.Sprintf("batch-test-%s", uuid.NewString()), Host: "localhost", Port: 5432,
		DBName: "target", Username: "u", PasswordEnc: []byte{1}, SSLMode: "disable",
	}
	if err := db.Create(&conn).Error; err != nil {
		t.Fatalf("connection: %v", err)
	}
	ids := make([]string, drafts)
	for i := range ids {
		m := models.Migration{ConnectionID: conn.ID, Name: fmt.Sprintf("m%d", i), SQL: "SELECT 1", Status: status, CreatedBy: "u"}
		if err := db.Create(&m).Error; err != nil {
			t.Fatalf("migration: %v", err)
		}
		ids[i] = m.ID
	}
	t.Cleanup(func() {
		db.Where("connection_id = ?", conn.ID).Delete(&models.MigrationBatch{})
		db.Where("connection_id = ?", conn.ID).Delete(&models.Migration{})
		db.Delete(&models.DBConnection{}, "id = ?", conn.ID)
	})
	return conn.ID, ids
}

func waitTerminal(t *testing.T, store *Store, id string) *models.MigrationBatch {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, err := store.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get batch: %v", err)
		}
		switch b.Status {
		case models.BatchSuccess, models.BatchPartial, models.BatchFailed, models.BatchCancelled:
			return b
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("batch did not finish")
	return nil
}

func TestCoordinatorSuccessAndPartial(t *testing.T) {
	db := batchTestDB(t)
	connID, ids := seed(t, db, 3, models.MigrationDraft)

	exec := &fakeExecutor{fail: map[string]bool{ids[1]: true}}
	c := New(db, exec)
	b, err := c.Create(context.Background(), connID, "tester", ids)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Start(b); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitTerminal(t, c.Store(), b.ID)
	if done.Status != models.BatchPartial || done.Processed != 2 || done.Failed != 1 {
		t.Fatalf("batch = %+v", done)
	}
	items, err := c.Store().Items(context.Background(), b.ID, 0, 10)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	if len(items) != 3 || items[1].Status != models.BatchItemFailed || items[0].Status != models.BatchItemSuccess {
		t.Fatalf("items = %+v", items)
	}
	if items[0].ExecutionID == "" || items[0].Attempt != 1 {
		t.Fatalf("audit row = %+v", items[0])
	}
}

func TestCreateRejectsNonDraft(t *testing.T) {
	db := batchTestDB(t)
	connID, ids := seed(t, db, 1, models.MigrationSuccess)
	c := New(db, &fakeExecutor{})
	if _, err := c.Create(context.Background(), connID, "tester", ids); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("err = %v, want ErrNotDraft", err)
	}
}

func TestRetryOnlyPendingItems(t *testing.T) {
	db := batchTestDB(t)
	connID, ids := seed(t, db, 2, models.MigrationDraft)
	c := New(db, &fakeExecutor{})
	b, err := c.Create(context.Background(), connID, "tester", ids)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Start(b); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitTerminal(t, c.Store(), b.ID)

	// Pretend the second item never ran so retry has exactly one candidate.
	if err := db.Model(&models.MigrationBatchItem{}).
		Where("batch_id = ? AND migration_id = ?", b.ID, ids[0]).
		Update("status", models.BatchItemSkipped).Error; err != nil {
		t.Fatalf("mark skipped: %v", err)
	}
	retried, err := c.Retry(context.Background(), b, "tester")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	items := mustItems(t, c.Store(), retried.ID)
	if len(items) != 1 || items[0].MigrationID != ids[0] {
		t.Fatalf("retry items = %+v", items)
	}
	if items[0].Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", items[0].Attempt)
	}
}

func mustItems(t *testing.T, store *Store, batchID string) []models.MigrationBatchItem {
	t.Helper()
	items, err := store.Items(context.Background(), batchID, 0, 100)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	return items
}
