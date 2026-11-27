package databasehandler_test

import (
	"testing"
	"time"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Cancel setelah batch selesai harus membuat change request revert, bukan
// menghentikan worker; revert/resume mengikuti aturan approval yang sama.
func TestIntegrationBatchCancelAndRevertRoutes(t *testing.T) {
	e := setup(t)
	leaderA, leaderB := e.assignLeaders()
	rows := pgx.Identifier{e.schema, "cancel_rows"}.Sanitize()
	e.execTarget(
		"CREATE TABLE "+rows+" (id bigint PRIMARY KEY, val int NOT NULL DEFAULT 0)",
		"INSERT INTO "+rows+" (id) SELECT g FROM generate_series(1, 250) g",
	)
	up := "UPDATE " + rows + " SET val = val + 1 WHERE id IN (SELECT id FROM " + rows +
		" WHERE id > :last_id ORDER BY id LIMIT :batch_size)"
	down := "UPDATE " + rows + " SET val = val - 1 WHERE id >= :from_id AND id <= :to_id"

	createBatch := func(name string) models.Migration {
		t.Helper()
		var m models.Migration
		body := map[string]any{"name": name, "sql": up, "sql_down": down, "batch_size": 100}
		if status, raw := e.do(e.editor, "POST", e.dbPath("migrations"), body, &m); status != fiber.StatusCreated {
			t.Fatalf("create %s: status %d body %s", name, status, raw)
		}
		return m
	}
	runToSuccess := func(m models.Migration) {
		t.Helper()
		status, cr, raw := e.submit(e.editor, m.ID, models.JobKindUp)
		if status != fiber.StatusCreated {
			t.Fatalf("submit %s: status %d body %s", m.Name, status, raw)
		}
		if job := e.approveAndWait(cr, leaderA, leaderB); job.Status != models.JobSuccess {
			t.Fatalf("job %s = %+v", m.Name, job)
		}
	}

	decide := func(method, user, mid, action string) (int, string) {
		t.Helper()
		return e.do(user, method, e.dbPath("migrations", mid, "batch", action), nil, nil)
	}

	// Batch yang sudah selesai: cancel membuat change request revert.
	done := createBatch("cancel-after-success")
	runToSuccess(done)
	var revertCR models.ChangeRequest
	if status, raw := e.do(e.editor, "POST", e.dbPath("migrations", done.ID, "batch", "cancel"), nil, &revertCR); status != fiber.StatusCreated ||
		revertCR.Kind != models.JobKindRevert || revertCR.Status != models.ChangeRequestPendingReview {
		t.Fatalf("cancel setelah sukses: status %d body %s", status, raw)
	}

	// Revert eksplisit juga membuat change request, bukan eksekusi langsung.
	revertable := createBatch("revert-explicit")
	runToSuccess(revertable)
	var cr models.ChangeRequest
	if status, raw := e.do(e.editor, "POST", e.dbPath("migrations", revertable.ID, "batch", "revert"), nil, &cr); status != fiber.StatusCreated ||
		cr.Kind != models.JobKindRevert {
		t.Fatalf("revert: status %d body %s", status, raw)
	}

	// Resume hanya untuk migration partial.
	if status, _ := decide("POST", e.editor, revertable.ID, "resume"); status != fiber.StatusConflict {
		t.Errorf("resume setelah sukses = %d, mau 409", status)
	}

	// Bukan migration batch.
	plain := e.createMigration(e.editor, "plain-migration", "SELECT 1")
	if status, body := decide("POST", e.editor, plain.ID, "cancel"); status != fiber.StatusBadRequest || body == "" {
		t.Errorf("cancel non-batch = %d, mau 400", status)
	}
	if status, _ := decide("POST", e.editor, plain.ID, "revert"); status != fiber.StatusBadRequest {
		t.Errorf("revert non-batch = %d, mau 400", status)
	}

	// Batch yang sedang berjalan: cancel hanya meminta worker berhenti.
	running := createBatch("running-batch")
	if err := e.db.Model(&models.Migration{}).Where("id = ?", running.ID).Update("batch_status", "running").Error; err != nil {
		t.Fatalf("set running: %v", err)
	}
	var accepted map[string]string
	if status, raw := e.do(e.editor, "POST", e.dbPath("migrations", running.ID, "batch", "cancel"), nil, &accepted); status != fiber.StatusAccepted ||
		accepted["status"] != "stop_requested" {
		t.Fatalf("cancel saat running: status %d body %s", status, raw)
	}
	var got models.Migration
	if err := e.db.First(&got, "id = ?", running.ID).Error; err != nil || !got.BatchCancelRequested {
		t.Errorf("flag cancel tidak tersimpan: %+v err %v", got, err)
	}

	// Hanya user dengan akses edit yang boleh menghentikan/membatalkan.
	queued := createBatch("viewer-batch")
	if status, _ := decide("POST", e.viewer, queued.ID, "cancel"); status != fiber.StatusForbidden {
		t.Errorf("cancel oleh viewer = %d, mau 403", status)
	}
	if status, _ := decide("POST", e.viewer, queued.ID, "revert"); status != fiber.StatusForbidden {
		t.Errorf("revert oleh viewer = %d, mau 403", status)
	}
}

// Cancel saat job masih queued meminta worker berhenti di batas potongan, dan
// revert ditolak lebih awal kalau checkpoint tidak aman.
func TestIntegrationBatchCancelQueuedAndUnsafeRevert(t *testing.T) {
	e := setup(t)
	rows := pgx.Identifier{e.schema, "queued_rows"}.Sanitize()
	e.execTarget(
		"CREATE TABLE "+rows+" (id bigint PRIMARY KEY, val int NOT NULL DEFAULT 0)",
		"INSERT INTO "+rows+" (id) SELECT g FROM generate_series(1, 100) g",
	)
	up := "UPDATE " + rows + " SET val = val + 1 WHERE id IN (SELECT id FROM " + rows +
		" WHERE id > :last_id ORDER BY id LIMIT :batch_size)"
	down := "UPDATE " + rows + " SET val = val - 1 WHERE id >= :from_id AND id <= :to_id"
	create := func(name string) models.Migration {
		t.Helper()
		var m models.Migration
		body := map[string]any{"name": name, "sql": up, "sql_down": down, "batch_size": 50}
		if status, raw := e.do(e.editor, "POST", e.dbPath("migrations"), body, &m); status != fiber.StatusCreated {
			t.Fatalf("create %s: status %d body %s", name, status, raw)
		}
		return m
	}

	queued := create("queued-cancel")
	job := models.MigrationJob{
		ConnectionID: e.connectionID, MigrationID: queued.ID, ExecutionID: uuid.NewString(),
		Kind: models.JobKindUp, Status: models.JobQueued, RunAfter: time.Now(),
		RequestedBy: e.editor, MaxAttempts: 3,
	}
	if err := e.db.Create(&job).Error; err != nil {
		t.Fatalf("seed queued job: %v", err)
	}
	var accepted map[string]string
	if status, raw := e.do(e.editor, "POST", e.dbPath("migrations", queued.ID, "batch", "cancel"), nil, &accepted); status != fiber.StatusAccepted ||
		accepted["status"] != "stop_requested" {
		t.Fatalf("cancel saat queued: status %d body %s", status, raw)
	}
	var got models.Migration
	if err := e.db.First(&got, "id = ?", queued.ID).Error; err != nil || !got.BatchCancelRequested {
		t.Fatalf("flag cancel tidak tersimpan: %+v err %v", got, err)
	}

	unsafe := create("unsafe-revert")
	if err := e.db.Model(&models.Migration{}).Where("id = ?", unsafe.ID).Updates(map[string]any{
		"status": models.MigrationPartial, "batch_run_id": uuid.NewString(),
	}).Error; err != nil {
		t.Fatalf("seed unsafe: %v", err)
	}
	if status, raw := e.do(e.editor, "POST", e.dbPath("migrations", unsafe.ID, "batch", "revert"), nil, nil); status != fiber.StatusConflict {
		t.Fatalf("revert tanpa checkpoint: status %d body %s, mau 409", status, raw)
	}
}
