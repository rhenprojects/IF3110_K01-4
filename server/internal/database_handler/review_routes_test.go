package databasehandler_test

import (
	"context"
	"fmt"
	"net/url"
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

func TestIntegrationBatchResumeRequiresApproval(t *testing.T) {
	e := setup(t)
	leaderA, leaderB := e.assignLeaders()
	rows := pgx.Identifier{e.schema, "resume_rows"}.Sanitize()
	gate := pgx.Identifier{e.schema, "resume_gate"}.Sanitize()
	e.execTarget(
		"CREATE TABLE "+rows+" (id bigint PRIMARY KEY, val int NOT NULL DEFAULT 0)",
		"INSERT INTO "+rows+" (id) SELECT g FROM generate_series(1, 300) g",
		"CREATE TABLE "+gate+" (open boolean NOT NULL)",
		"INSERT INTO "+gate+" VALUES (false)",
	)
	up := "UPDATE " + rows + " SET val = val + 1 / (CASE WHEN id > 100 AND NOT (SELECT open FROM " + gate + ") THEN 0 ELSE 1 END)" +
		" WHERE id IN (SELECT id FROM " + rows + " WHERE id > :last_id ORDER BY id LIMIT :batch_size)"
	down := "UPDATE " + rows + " SET val = val - 1 WHERE id >= :from_id AND id <= :to_id"
	var m models.Migration
	if status, raw := e.do(e.editor, "POST", e.dbPath("migrations"),
		map[string]any{"name": "resume batch", "sql": up, "sql_down": down, "batch_size": 100}, &m); status != fiber.StatusCreated {
		t.Fatalf("create batch migration: status %d body %s", status, raw)
	}
	status, cr, raw := e.submit(e.editor, m.ID, models.JobKindUp)
	if status != fiber.StatusCreated {
		t.Fatalf("submit up: status %d body %s", status, raw)
	}
	if first := e.approveAndWait(cr, leaderA, leaderB); first.Status != models.JobFailed {
		t.Fatalf("first run = %+v, want failed at second chunk", first)
	}
	reload := func() models.Migration {
		var got models.Migration
		if status, raw := e.do(e.viewer, "GET", e.dbPath("migrations", m.ID), nil, &got); status != fiber.StatusOK {
			t.Fatalf("get migration: status %d body %s", status, raw)
		}
		return got
	}
	if got := reload(); got.Status != models.MigrationPartial || got.BatchRows != 100 || got.BatchChunks != 1 {
		t.Fatalf("after failed chunk = %+v, want partial checkpoint at 100 rows", got)
	}

	e.execTarget("UPDATE " + gate + " SET open = true")
	resumePath := e.dbPath("migrations", m.ID, "batch", "resume")
	if status, raw := e.do(e.viewer, "POST", resumePath, nil, nil); status != fiber.StatusForbidden {
		t.Errorf("viewer resume: status %d body %s, want 403", status, raw)
	}
	var resume models.ChangeRequest
	if status, raw := e.do(e.editor, "POST", resumePath, nil, &resume); status != fiber.StatusCreated ||
		resume.Status != models.ChangeRequestPendingReview || resume.Kind != models.JobKindUp || resume.JobID != nil {
		t.Fatalf("resume: status %d body %s, want pending change request", status, raw)
	}
	var waiting int64
	if err := e.db.Model(&models.MigrationJob{}).Where("migration_id = ? AND status IN ?", m.ID,
		[]models.JobStatus{models.JobQueued, models.JobRunning}).Count(&waiting).Error; err != nil || waiting != 0 {
		t.Fatalf("resume queued before approval: %d jobs, err %v", waiting, err)
	}
	if status, _ := e.do(e.editor, "POST", resumePath, nil, nil); status != fiber.StatusConflict {
		t.Errorf("duplicate resume = %d, want 409", status)
	}
	if got := reload(); got.Status != models.MigrationPartial {
		t.Errorf("status during review = %s, want partial", got.Status)
	}

	if done := e.approveAndWait(resume, leaderA, leaderB); done.Status != models.JobSuccess {
		t.Fatalf("resumed job = %+v", done)
	}
	if got := reload(); got.Status != models.MigrationSuccess || got.BatchRows != 300 || got.BatchChunks != 3 {
		t.Errorf("after resume = %+v, want success with 300 rows in 3 chunks", got)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var wrong int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+rows+" WHERE val <> 1").Scan(&wrong); err != nil || wrong != 0 {
		t.Errorf("rows updated more or less than once: %d, err %v", wrong, err)
	}
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

func TestIntegrationChangeRequestProgressAndInbox(t *testing.T) {
	e := setup(t)
	leaderA, leaderB := e.assignLeaders()
	table := pgx.Identifier{e.schema, "cr_inbox"}.Sanitize()
	m := e.createMigration(e.editor, "add cr_inbox", "CREATE TABLE IF NOT EXISTS "+table+" (id int)")

	status, cr, raw := e.submit(e.editor, m.ID, models.JobKindUp)
	if status != fiber.StatusCreated {
		t.Fatalf("submit: status %d body %s", status, raw)
	}
	if cr.MigrationName != "add cr_inbox" || len(cr.Reviewers) != 2 || cr.CanDecide {
		t.Errorf("respons submit = %+v", cr)
	}
	for _, r := range cr.Reviewers {
		if r.Decision != models.ReviewerPending || r.LeaderID == nil || r.GroupName == "" {
			t.Errorf("reviewer = %+v", r)
		}
	}

	inbox := func(user string) []models.ChangeRequest {
		t.Helper()
		var resp models.ChangeRequestListResponse
		if status, raw := e.do(user, "GET", "/change-requests/pending", nil, &resp); status != fiber.StatusOK {
			t.Fatalf("inbox: status %d body %s", status, raw)
		}
		return resp.ChangeRequests
	}
	if status, _ := e.do("", "GET", "/change-requests/pending", nil, nil); status != fiber.StatusUnauthorized {
		t.Errorf("inbox tanpa login = %d, mau 401", status)
	}
	if got := inbox(e.editor); len(got) != 0 {
		t.Errorf("inbox non-leader = %+v", got)
	}
	got := inbox(leaderA)
	if len(got) != 1 || got[0].ID != cr.ID || !got[0].CanDecide || got[0].ConnectionName == "" || got[0].RequestedByName == "" {
		t.Fatalf("inbox leader A = %+v", got)
	}

	status, first, raw := e.decide(leaderA, cr.ID, "approve")
	if status != fiber.StatusOK || first.Status != models.ChangeRequestPendingReview || first.CanDecide {
		t.Fatalf("approve leader A: status %d body %s", status, raw)
	}
	approvedBy := 0
	for _, r := range first.Reviewers {
		if r.Decision == "approve" {
			approvedBy++
		}
	}
	if approvedBy != 1 {
		t.Errorf("progres setelah leader A = %+v", first.Reviewers)
	}
	if got := inbox(leaderA); len(got) != 0 {
		t.Errorf("inbox leader A setelah approve = %+v", got)
	}
	if got := inbox(leaderB); len(got) != 1 {
		t.Errorf("inbox leader B = %+v", got)
	}

	list := func(query string) (int, []models.ChangeRequest) {
		var resp models.ChangeRequestListResponse
		status, _ := e.do(e.viewer, "GET", e.dbPath("change-requests")+query, nil, &resp)
		return status, resp.ChangeRequests
	}
	if status, got := list("?status=pending_review"); status != fiber.StatusOK || len(got) != 1 || len(got[0].Reviewers) != 2 {
		t.Errorf("filter pending_review = %d %+v", status, got)
	}
	if status, got := list("?status=approved"); status != fiber.StatusOK || len(got) != 0 {
		t.Errorf("filter approved = %d %+v", status, got)
	}
	if status, _ := list("?status=bogus"); status != fiber.StatusBadRequest {
		t.Errorf("filter tidak valid = %d, mau 400", status)
	}

	status, done, raw := e.decide(leaderB, cr.ID, "approve")
	if status != fiber.StatusOK || done.Status != models.ChangeRequestApproved || done.JobID == nil {
		t.Fatalf("approve leader B: status %d body %s", status, raw)
	}
	e.waitJob(*done.JobID)
	if got := inbox(leaderB); len(got) != 0 {
		t.Errorf("inbox setelah selesai = %+v", got)
	}
	if status, got := list("?status=approved"); status != fiber.StatusOK || len(got) != 1 || got[0].ID != cr.ID {
		t.Errorf("filter approved setelah selesai = %d %+v", status, got)
	}
}

func TestIntegrationDispatchChangeRequest(t *testing.T) {
	e := setup(t)
	leaderA, leaderB := e.assignLeaders()
	table := pgx.Identifier{e.schema, "cr_dispatch"}.Sanitize()
	m := e.createMigration(e.editor, "add cr_dispatch", "CREATE TABLE IF NOT EXISTS "+table+" (id int)")
	_, cr, _ := e.submit(e.editor, m.ID, models.JobKindUp)

	dispatch := func(user, requestID string) (int, models.ChangeRequest) {
		var got models.ChangeRequest
		status, _ := e.do(user, "POST", e.dbPath("change-requests", requestID, "dispatch"), nil, &got)
		return status, got
	}
	if status, _ := dispatch(e.editor, cr.ID); status != fiber.StatusConflict {
		t.Errorf("dispatch sebelum approved = %d, mau 409", status)
	}
	if status, _ := dispatch(e.viewer, cr.ID); status != fiber.StatusForbidden {
		t.Errorf("dispatch oleh viewer = %d, mau 403", status)
	}
	if status, _ := dispatch(e.outsider, cr.ID); status != fiber.StatusForbidden {
		t.Errorf("dispatch tanpa akses = %d, mau 403", status)
	}
	if status, _ := dispatch(e.editor, uuid.NewString()); status != fiber.StatusNotFound {
		t.Errorf("dispatch permintaan tidak ada = %d, mau 404", status)
	}

	job := e.approveAndWait(cr, leaderA, leaderB)
	status, again := dispatch(e.editor, cr.ID)
	if status != fiber.StatusOK || again.JobID == nil || *again.JobID != job.ID {
		t.Errorf("dispatch ulang harus idempoten: status %d, %+v", status, again)
	}
	if len(again.Reviewers) != 2 {
		t.Errorf("respons dispatch harus diperkaya: %+v", again)
	}
}

type bulkApproveResponse struct {
	Results []struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	} `json:"results"`
}

func (e *env) bulkApprove(userID string, ids ...string) (int, bulkApproveResponse, string) {
	e.t.Helper()
	return e.bulkApproveIn(e.connectionID, userID, ids...)
}

// bulkApproveIn memanggil bulk approve pada database tertentu.
func (e *env) bulkApproveIn(connectionID, userID string, ids ...string) (int, bulkApproveResponse, string) {
	e.t.Helper()
	var resp bulkApproveResponse
	path := "/databases/" + url.PathEscape(connectionID) + "/change-requests/approve"
	status, raw := e.do(userID, "POST", path, map[string]any{"ids": ids, "comment": "bulk"}, &resp)
	return status, resp, raw
}

// Bulk approve tetap memutuskan tiap permintaan sendiri-sendiri: satu kegagalan
// dilaporkan per item dan tidak membatalkan item lain.
func TestIntegrationBulkApproveChangeRequests(t *testing.T) {
	e := setup(t)
	leaderA, leaderB := e.assignLeaders()
	create := func(name string) models.Migration {
		table := pgx.Identifier{e.schema, name}.Sanitize()
		return e.createMigration(e.editor, "add "+name, "CREATE TABLE IF NOT EXISTS "+table+" (id int)")
	}
	m1, m2 := create("bulk_a"), create("bulk_b")
	_, cr1, _ := e.submit(e.editor, m1.ID, models.JobKindUp)
	_, cr2, _ := e.submit(e.editor, m2.ID, models.JobKindUp)

	// Non-leader: kedua item gagal, tetap 200 supaya hasil per item terbaca.
	status, resp, raw := e.bulkApprove(e.viewer, cr1.ID, cr2.ID)
	if status != fiber.StatusOK || len(resp.Results) != 2 || resp.Results[0].Error == "" || resp.Results[1].Error == "" {
		t.Fatalf("bulk non-leader: status %d body %s", status, raw)
	}
	if got := e.requestStatus(cr1.ID); got != models.ChangeRequestPendingReview {
		t.Fatalf("status setelah bulk non-leader = %s, mau pending_review", got)
	}

	// Leader A menyetujui keduanya: masih menunggu leader B, belum ada job.
	status, resp, raw = e.bulkApprove(leaderA, cr1.ID, cr2.ID)
	if status != fiber.StatusOK || len(resp.Results) != 2 {
		t.Fatalf("bulk leader A: status %d body %s", status, raw)
	}
	for _, r := range resp.Results {
		if r.Error != "" {
			t.Errorf("leader A gagal di %s: %s", r.ID, r.Error)
		}
	}
	if got := e.requestStatus(cr1.ID); got != models.ChangeRequestPendingReview {
		t.Errorf("status setelah leader A = %s, mau pending_review", got)
	}

	// Permintaan yang sudah diputuskan dan id asing dilaporkan per item.
	status, resp, _ = e.bulkApprove(leaderA, cr1.ID, uuid.NewString())
	if status != fiber.StatusOK || resp.Results[0].Error == "" || resp.Results[1].Error == "" {
		t.Errorf("bulk dengan item gagal: status %d %+v", status, resp)
	}

	// Leader B: keduanya disetujui penuh lalu masuk queue.
	status, resp, raw = e.bulkApprove(leaderB, cr1.ID, cr2.ID)
	if status != fiber.StatusOK {
		t.Fatalf("bulk leader B: status %d body %s", status, raw)
	}
	for _, r := range resp.Results {
		if r.Error != "" {
			t.Fatalf("leader B gagal di %s: %s", r.ID, r.Error)
		}
	}
	var list models.ChangeRequestListResponse
	if status, raw := e.do(e.viewer, "GET", e.dbPath("change-requests"), nil, &list); status != fiber.StatusOK {
		t.Fatalf("list: status %d body %s", status, raw)
	}
	if len(list.ChangeRequests) != 2 {
		t.Fatalf("jumlah change request = %d", len(list.ChangeRequests))
	}
	for _, cr := range list.ChangeRequests {
		if cr.Status != models.ChangeRequestApproved || cr.JobID == nil {
			t.Fatalf("change request %s = %+v, mau approved + job", cr.MigrationName, cr)
		}
		if job := e.waitJob(*cr.JobID); job.Status != models.JobSuccess {
			t.Fatalf("job %s = %+v", cr.MigrationName, job)
		}
	}

	// Validasi bentuk permintaan.
	if status, _ := e.do(leaderA, "POST", e.dbPath("change-requests", "approve"), map[string]any{"ids": []string{}}, nil); status != fiber.StatusBadRequest {
		t.Errorf("bulk tanpa id = %d, mau 400", status)
	}
	// Database lain bukan error transport: item dilaporkan gagal satu per satu.
	status, resp, _ = e.bulkApproveIn(uuid.NewString(), leaderA, cr1.ID)
	if status != fiber.StatusOK || len(resp.Results) != 1 || resp.Results[0].Error == "" {
		t.Errorf("bulk lewat database lain = %d %+v", status, resp)
	}
	if status, _ := e.do(leaderA, "POST", "/databases/bukan-uuid/change-requests/approve", map[string]any{"ids": []string{cr1.ID}}, nil); status != fiber.StatusNotFound {
		t.Errorf("bulk dengan id database tidak valid = %d, mau 404", status)
	}
}

// requestStatus membaca status satu change request dari database.
func (e *env) requestStatus(requestID string) models.ChangeRequestStatus {
	e.t.Helper()
	var cr models.ChangeRequest
	if err := e.db.First(&cr, "id = ?", requestID).Error; err != nil {
		e.t.Fatalf("load change request: %v", err)
	}
	return cr.Status
}
