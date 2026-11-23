package databasehandler_test

import (
	"fmt"
	"testing"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dua group (editor dan viewer) masing-masing dipimpin leader sendiri; keduanya harus approve.
func (e *env) assignLeaders() (leaderA, leaderB string) {
	e.t.Helper()
	leaderA, leaderB = e.createUser("leader-a-"+e.schema), e.createUser("leader-b-"+e.schema)
	e.setLeaders(leaderA, leaderB)
	return leaderA, leaderB
}

// setLeaders menetapkan leader group editor dan group viewer (berurutan).
func (e *env) setLeaders(editorGroupLeader, viewerGroupLeader string) {
	e.t.Helper()
	for i, leader := range []string{editorGroupLeader, viewerGroupLeader} {
		if err := e.db.Model(&models.Group{}).Where("id = ?", e.groupIDs[i]).Update("leader_id", leader).Error; err != nil {
			e.t.Fatalf("set leader: %v", err)
		}
	}
}

func (e *env) submit(userID, migrationID string, kind models.JobKind) (int, models.ChangeRequest, string) {
	e.t.Helper()
	var cr models.ChangeRequest
	status, raw := e.do(userID, "POST", e.dbPath("migrations", migrationID, "change-requests"), map[string]string{"kind": string(kind)}, &cr)
	return status, cr, raw
}

func (e *env) decide(userID, requestID, action string) (int, models.ChangeRequest, string) {
	e.t.Helper()
	var cr models.ChangeRequest
	status, raw := e.do(userID, "POST", e.dbPath("change-requests", requestID, action), map[string]string{"comment": "ok"}, &cr)
	return status, cr, raw
}

// approveAndWait menyetujui sebagai kedua leader dan menunggu job hasil dispatch selesai.
func (e *env) approveAndWait(cr models.ChangeRequest, leaderA, leaderB string) models.MigrationJob {
	e.t.Helper()
	if status, got, raw := e.decide(leaderA, cr.ID, "approve"); status != fiber.StatusOK || got.Status != models.ChangeRequestPendingReview || got.JobID != nil {
		e.t.Fatalf("approve leader A: status %d body %s", status, raw)
	}
	status, got, raw := e.decide(leaderB, cr.ID, "approve")
	if status != fiber.StatusOK || got.Status != models.ChangeRequestApproved || got.JobID == nil {
		e.t.Fatalf("approve leader B: status %d body %s", status, raw)
	}
	return e.waitJob(*got.JobID)
}

func (e *env) history(hide bool) []models.MigrationExecution {
	e.t.Helper()
	var resp models.ExecutionListResponse
	path := e.dbPath("history")
	if hide {
		path += "?hide_rollback=true"
	}
	if status, raw := e.do(e.viewer, "GET", path, nil, &resp); status != fiber.StatusOK {
		e.t.Fatalf("history: status %d body %s", status, raw)
	}
	return resp.Executions
}

func TestIntegrationChangeRequestApprovalFlow(t *testing.T) {
	e := setup(t)
	leaderA, leaderB := e.assignLeaders()
	table := pgx.Identifier{e.schema, "cr_flow"}.Sanitize()
	m := e.createMigration(e.editor, "add cr_flow", "CREATE TABLE IF NOT EXISTS "+table+" (id int)")

	if status, _, _ := e.submit(e.viewer, m.ID, models.JobKindUp); status != fiber.StatusForbidden {
		t.Errorf("viewer submit = %d, mau 403", status)
	}
	if status, _, _ := e.submit(e.outsider, m.ID, models.JobKindUp); status != fiber.StatusForbidden {
		t.Errorf("outsider submit = %d, mau 403", status)
	}
	if status, _, _ := e.submit(e.editor, m.ID, "bogus"); status != fiber.StatusBadRequest {
		t.Errorf("kind tidak dikenal = %d, mau 400", status)
	}
	if status, _, _ := e.submit(e.editor, uuid.NewString(), models.JobKindUp); status != fiber.StatusNotFound {
		t.Errorf("migration tidak ada = %d, mau 404", status)
	}

	status, cr, raw := e.submit(e.editor, m.ID, models.JobKindUp)
	if status != fiber.StatusCreated || cr.Status != models.ChangeRequestPendingReview {
		t.Fatalf("submit up: status %d body %s", status, raw)
	}
	if status, _, _ := e.submit(e.editor, m.ID, models.JobKindUp); status != fiber.StatusConflict {
		t.Errorf("submit ganda = %d, mau 409", status)
	}
	if status, _, _ := e.decide(e.editor, cr.ID, "approve"); status != fiber.StatusForbidden {
		t.Errorf("non-leader approve = %d, mau 403", status)
	}
	if status, _ := e.do(leaderA, "POST", "/databases/"+uuid.NewString()+"/change-requests/"+cr.ID+"/approve", nil, nil); status != fiber.StatusNotFound {
		t.Errorf("approve lewat database lain = %d, mau 404", status)
	}

	if job := e.approveAndWait(cr, leaderA, leaderB); job.Status != models.JobSuccess || job.Kind != models.JobKindUp {
		t.Fatalf("job up = %+v", job)
	}
	if !e.targetTableExists("cr_flow") {
		t.Error("tabel hasil migration up tidak ada")
	}
	if status, _, _ := e.decide(leaderA, cr.ID, "approve"); status != fiber.StatusConflict {
		t.Errorf("approve permintaan yang sudah diputuskan = %d, mau 409", status)
	}

	var list models.ChangeRequestListResponse
	if status, raw := e.do(e.viewer, "GET", e.dbPath("change-requests"), nil, &list); status != fiber.StatusOK || len(list.ChangeRequests) != 1 || list.ChangeRequests[0].ID != cr.ID {
		t.Errorf("list change requests: status %d body %s", status, raw)
	}
	if status, _ := e.do(e.outsider, "GET", e.dbPath("change-requests"), nil, nil); status != fiber.StatusForbidden {
		t.Errorf("list tanpa akses = %d, mau 403", status)
	}

	// Rollback lalu forward: keduanya lewat review dan queue.
	status, rb, raw := e.submit(e.editor, m.ID, models.JobKindRollback)
	if status != fiber.StatusCreated || rb.RollbackOf == nil {
		t.Fatalf("submit rollback: status %d body %s", status, raw)
	}
	if job := e.approveAndWait(rb, leaderA, leaderB); job.Status != models.JobSuccess || job.Kind != models.JobKindRollback {
		t.Fatalf("job rollback = %+v", job)
	}
	if status, _, _ := e.submit(e.editor, m.ID, models.JobKindRollback); status != fiber.StatusConflict {
		t.Errorf("rollback migration yang sudah tidak aktif = %d, mau 409", status)
	}
	status, fw, raw := e.submit(e.editor, m.ID, models.JobKindForward)
	if status != fiber.StatusCreated {
		t.Fatalf("submit forward: status %d body %s", status, raw)
	}
	if job := e.approveAndWait(fw, leaderA, leaderB); job.Status != models.JobSuccess || job.Kind != models.JobKindForward {
		t.Fatalf("job forward = %+v", job)
	}

	if full := e.history(false); len(full) != 3 {
		t.Fatalf("riwayat lengkap = %d attempt, mau 3 (up, rollback, forward)", len(full))
	}
	hidden := e.history(true)
	if len(hidden) != 1 || hidden[0].Kind != models.JobKindForward {
		t.Fatalf("hide rollback = %s, mau hanya forward", fmt.Sprint(hidden))
	}
}

func TestIntegrationChangeRequestReject(t *testing.T) {
	e := setup(t)
	leaderA, _ := e.assignLeaders()
	m := e.createMigration(e.editor, "rejected", "SELECT 1")
	_, cr, _ := e.submit(e.editor, m.ID, models.JobKindUp)

	if status, got, raw := e.decide(leaderA, cr.ID, "reject"); status != fiber.StatusOK || got.Status != models.ChangeRequestRejected {
		t.Fatalf("reject: status %d body %s", status, raw)
	}
	var got models.Migration
	if status, _ := e.do(e.editor, "GET", e.dbPath("migrations", m.ID), nil, &got); status != fiber.StatusOK || got.Status != models.MigrationDraft {
		t.Errorf("migration setelah ditolak = %s, mau draft", got.Status)
	}
}

func TestIntegrationChangeRequestRequiresLeaders(t *testing.T) {
	e := setup(t) // group belum punya leader
	m := e.createMigration(e.editor, "no leader", "SELECT 1")
	if status, _, raw := e.submit(e.editor, m.ID, models.JobKindUp); status != fiber.StatusConflict {
		t.Errorf("submit tanpa leader = %d, mau 409 (%s)", status, raw)
	}
}
