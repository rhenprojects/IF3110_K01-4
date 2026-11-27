package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"web_backend/internal/models"
	"web_backend/internal/workflow"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var errInvalidDefinition = errors.New("invalid definition")

type fakeQueue struct {
	db    *gorm.DB
	mu    sync.Mutex
	calls []models.ChangeRequest
	err   error
}

// enqueue membuat baris job sungguhan seperti engine, sehingga pencarian job dan aturan
// "satu job aktif per migration" ikut teruji.
func (q *fakeQueue) enqueue(_ context.Context, cr models.ChangeRequest) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return "", q.err
	}
	job := models.MigrationJob{
		ConnectionID: cr.ConnectionID, MigrationID: cr.MigrationID, ExecutionID: uuid.NewString(),
		Kind: cr.Kind, Status: models.JobQueued, RollbackOf: cr.RollbackOf, RequestedBy: cr.RequestedBy,
	}
	if err := q.db.Create(&job).Error; err != nil {
		return "", err
	}
	q.calls = append(q.calls, cr)
	return job.ID, nil
}

func validate(m *models.Migration) error {
	if m.SQLDown == "" && !m.Irreversible {
		return errInvalidDefinition
	}
	return nil
}

type env struct {
	t       *testing.T
	db      *gorm.DB
	q       *fakeQueue
	svc     *Service
	connID  string
	editor  string
	leaderA string
	leaderB string
	viewer  string
	outside string
	seq     int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	all := models.All()
	for i := len(all) - 1; i >= 0; i-- {
		if err := db.Migrator().DropTable(all[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := models.Migrate(db); err != nil {
		t.Fatal(err)
	}

	q := &fakeQueue{db: db}
	e := &env{t: t, db: db, q: q, svc: New(db, Deps{Enqueue: q.enqueue, Validate: validate})}
	conn := models.DBConnection{Name: "c1", Host: "h", DBName: "d", Username: "u", PasswordEnc: []byte("x")}
	e.create(&conn)
	e.connID = conn.ID
	e.editor, e.leaderA, e.leaderB = e.user("editor"), e.user("leader-a"), e.user("leader-b")
	e.viewer, e.outside = e.user("viewer"), e.user("outside")

	e.group("A", &e.leaderA, models.AccessEdit, e.editor, e.leaderA)
	e.group("B", &e.leaderB, models.AccessView, e.viewer, e.leaderB)
	return e
}

func (e *env) create(v any) {
	e.t.Helper()
	if err := e.db.Create(v).Error; err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) user(name string) string {
	u := models.User{Username: name, Name: name, Password: "x"}
	e.create(&u)
	return u.ID
}

func (e *env) group(name string, leader *string, level models.AccessLevel, members ...string) string {
	g := models.Group{Name: name, LeaderID: leader}
	e.create(&g)
	e.create(&models.GroupDatabaseAccess{GroupID: g.ID, ConnectionID: e.connID, Level: level})
	for _, m := range members {
		e.create(&models.GroupMember{GroupID: g.ID, UserID: m})
	}
	return g.ID
}

func (e *env) migration(status models.MigrationStatus, sqlDown string, irreversible bool) *models.Migration {
	e.seq++
	m := models.Migration{
		ConnectionID: e.connID, Name: fmt.Sprintf("m%d", e.seq), SQL: "SELECT 1",
		SQLDown: sqlDown, Irreversible: irreversible, CreatedBy: e.editor, Status: status,
	}
	if irreversible {
		m.IrreversibleReason = "data is dropped"
	}
	e.create(&m)
	return &m
}

func (e *env) execution(m *models.Migration, execID string, attempt int, kind models.JobKind, status models.ExecutionStatus, rollbackOf *string) {
	now := time.Now()
	e.create(&models.MigrationExecution{
		ExecutionID: execID, Attempt: attempt, ConnectionID: e.connID, MigrationID: m.ID,
		Kind: kind, Status: status, Revision: 1, SQL: "SELECT 1", RollbackOf: rollbackOf,
		ExecutedBy: e.editor, StartedAt: now, FinishedAt: now,
	})
}

func (e *env) migrationStatus(id string) models.MigrationStatus {
	var m models.Migration
	if err := e.db.First(&m, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return m.Status
}

func (e *env) request(id string) models.ChangeRequest {
	var cr models.ChangeRequest
	if err := e.db.First(&cr, "id = ?", id).Error; err != nil {
		e.t.Fatal(err)
	}
	return cr
}

func (e *env) submit(m *models.Migration, kind models.JobKind) *models.ChangeRequest {
	e.t.Helper()
	cr, err := e.svc.Submit(context.Background(), e.editor, e.connID, m.ID, kind)
	if err != nil {
		e.t.Fatalf("submit: %v", err)
	}
	return cr
}

func (e *env) approveAll(cr *models.ChangeRequest) {
	e.t.Helper()
	for _, leader := range []string{e.leaderA, e.leaderB} {
		if _, err := e.svc.Approve(context.Background(), leader, cr.ID, ""); err != nil {
			e.t.Fatalf("approve: %v", err)
		}
	}
}

var ctx = context.Background()

func TestSubmit_UpMovesMigrationToPendingReview(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationDraft, "DROP TABLE x", false)

	cr := e.submit(m, models.JobKindUp)

	if cr.Status != models.ChangeRequestPendingReview || cr.RequestedBy != e.editor {
		t.Errorf("change request = %+v", cr)
	}
	if got := e.migrationStatus(m.ID); got != models.MigrationPendingReview {
		t.Errorf("status migration = %s, mau pending_review", got)
	}
}

func TestSubmit_Rejections(t *testing.T) {
	e := newEnv(t)
	draft := e.migration(models.MigrationDraft, "DROP TABLE x", false)

	cases := []struct {
		name string
		user string
		mig  *models.Migration
		kind models.JobKind
		want error
	}{
		{"user view only", e.viewer, draft, models.JobKindUp, ErrEditAccessRequired},
		{"user tanpa akses", e.outside, draft, models.JobKindUp, ErrEditAccessRequired},
		{"forward user view only", e.viewer, draft, models.JobKindForward, ErrEditAccessRequired},
		{"kind ngawur", e.editor, draft, "bogus", ErrInvalidKind},
		{"migration sudah dieksekusi", e.editor, e.migration(models.MigrationSuccess, "x", false), models.JobKindUp, ErrNotDraft},
		{"definisi tidak lengkap", e.editor, e.migration(models.MigrationDraft, "", false), models.JobKindUp, errInvalidDefinition},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.svc.Submit(ctx, c.user, e.connID, c.mig.ID, c.kind)
			if !errors.Is(err, c.want) {
				t.Fatalf("error = %v, mau %v", err, c.want)
			}
		})
	}
	if got := e.migrationStatus(draft.ID); got != models.MigrationDraft {
		t.Errorf("pengajuan yang ditolak tidak boleh mengubah status, dapat %s", got)
	}
}

func TestSubmit_UnknownMigration(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{uuid.NewString(), "bukan-uuid"} {
		if _, err := e.svc.Submit(ctx, e.editor, e.connID, id, models.JobKindUp); !errors.Is(err, ErrMigrationNotFound) {
			t.Errorf("id %q: error = %v", id, err)
		}
	}
}

func TestSubmit_TwiceIsRejected(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationDraft, "DROP TABLE x", false)
	e.submit(m, models.JobKindUp)

	_, err := e.svc.Submit(ctx, e.editor, e.connID, m.ID, models.JobKindUp)
	if !errors.Is(err, ErrNotDraft) && !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("error = %v", err)
	}
}

func TestSubmit_GroupWithoutLeaderBlocksReview(t *testing.T) {
	e := newEnv(t)
	e.group("C", nil, models.AccessView)
	m := e.migration(models.MigrationDraft, "DROP TABLE x", false)

	_, err := e.svc.Submit(ctx, e.editor, e.connID, m.ID, models.JobKindUp)

	if !errors.Is(err, ErrGroupsWithoutLeader) {
		t.Fatalf("error = %v", err)
	}
	if got := e.migrationStatus(m.ID); got != models.MigrationDraft {
		t.Errorf("status harus tetap draft, dapat %s", got)
	}
}

func TestApprove_RequiresEveryLeader(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationDraft, "DROP TABLE x", false)
	cr := e.submit(m, models.JobKindUp)

	got, err := e.svc.Approve(ctx, e.leaderA, cr.ID, "ok")
	if err != nil || got.Status != models.ChangeRequestPendingReview {
		t.Fatalf("setelah leader A: %v, %+v", err, got)
	}
	if len(e.q.calls) != 0 || e.migrationStatus(m.ID) != models.MigrationPendingReview {
		t.Fatal("belum boleh dikirim ke queue sebelum semua leader setuju")
	}

	got, err = e.svc.Approve(ctx, e.leaderB, cr.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.ChangeRequestApproved || got.JobID == nil {
		t.Errorf("setelah semua leader: %+v", got)
	}
	if e.migrationStatus(m.ID) != models.MigrationApproved {
		t.Errorf("status migration = %s, mau approved", e.migrationStatus(m.ID))
	}
	if len(e.q.calls) != 1 || e.q.calls[0].Kind != models.JobKindUp || e.q.calls[0].MigrationID != m.ID {
		t.Errorf("panggilan queue = %+v", e.q.calls)
	}
}

func TestApprove_OnlyLeadersMayDecide(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)

	for name, user := range map[string]string{"editor": e.editor, "viewer": e.viewer, "outside": e.outside} {
		if _, err := e.svc.Approve(ctx, user, cr.ID, ""); !errors.Is(err, ErrNotLeader) {
			t.Errorf("%s: approve error = %v", name, err)
		}
		if _, err := e.svc.Reject(ctx, user, cr.ID, ""); !errors.Is(err, ErrNotLeader) {
			t.Errorf("%s: reject error = %v", name, err)
		}
	}
	if got := e.request(cr.ID).Status; got != models.ChangeRequestPendingReview {
		t.Errorf("status = %s", got)
	}
}

func TestApprove_Twice(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)

	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("approve ulang oleh leader yang sama: %v", err)
	}
	if _, err := e.svc.Approve(ctx, e.leaderB, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Approve(ctx, e.leaderB, cr.ID, ""); !errors.Is(err, ErrNotPending) {
		t.Errorf("approve setelah selesai: %v", err)
	}
	if len(e.q.calls) != 1 {
		t.Errorf("queue dipanggil %d kali, mau 1", len(e.q.calls))
	}
}

func TestApprove_UnknownRequest(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{uuid.NewString(), "bukan-uuid"} {
		if _, err := e.svc.Approve(ctx, e.leaderA, id, ""); !errors.Is(err, ErrRequestNotFound) {
			t.Errorf("id %q: %v", id, err)
		}
	}
}

func TestReject_ReturnsMigrationToDraftAndAllowsResubmit(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationDraft, "DROP TABLE x", false)
	cr := e.submit(m, models.JobKindUp)

	got, err := e.svc.Reject(ctx, e.leaderB, cr.ID, "kolom masih dipakai")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.ChangeRequestRejected || got.DecidedAt == nil {
		t.Errorf("change request = %+v", got)
	}
	if e.migrationStatus(m.ID) != models.MigrationDraft {
		t.Errorf("status migration = %s, mau draft", e.migrationStatus(m.ID))
	}
	if len(e.q.calls) != 0 {
		t.Error("penolakan tidak boleh mengirim ke queue")
	}
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); !errors.Is(err, ErrNotPending) {
		t.Errorf("approve setelah ditolak: %v", err)
	}

	if _, err := e.svc.Submit(ctx, e.editor, e.connID, m.ID, models.JobKindUp); err != nil {
		t.Errorf("pengajuan ulang harus boleh: %v", err)
	}
}

// partialBatch membuat migration batch yang berhenti di checkpoint.
func (e *env) partialBatch() *models.Migration {
	e.t.Helper()
	m := e.migration(models.MigrationPartial, "UPDATE t SET v = v - 1", false)
	runID := uuid.NewString()
	if err := e.db.Model(m).Updates(map[string]any{"batch_size": 100, "batch_run_id": runID, "batch_status": "partial"}).Error; err != nil {
		e.t.Fatal(err)
	}
	m.BatchSize, m.BatchRunID = 100, &runID
	return m
}

func TestResume_PartialBatchNeedsEveryLeader(t *testing.T) {
	e := newEnv(t)
	m := e.partialBatch()

	cr := e.submit(m, models.JobKindUp)
	if cr.Status != models.ChangeRequestPendingReview || len(e.q.calls) != 0 {
		t.Fatalf("resume harus menunggu review, bukan langsung masuk queue: %+v, calls %d", cr, len(e.q.calls))
	}
	if got := e.migrationStatus(m.ID); got != models.MigrationPartial {
		t.Errorf("status migration selama review = %s, mau tetap partial", got)
	}
	if _, err := e.svc.Submit(ctx, e.editor, e.connID, m.ID, models.JobKindUp); !errors.Is(err, ErrAlreadyPending) {
		t.Errorf("resume ganda: %v", err)
	}

	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	if len(e.q.calls) != 0 {
		t.Fatal("resume masuk queue sebelum semua leader setuju")
	}
	got, err := e.svc.Approve(ctx, e.leaderB, cr.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.ChangeRequestApproved || got.JobID == nil || len(e.q.calls) != 1 || e.q.calls[0].Kind != models.JobKindUp {
		t.Errorf("resume setelah semua setuju = %+v, calls %+v", got, e.q.calls)
	}
}

func TestResume_RejectKeepsCheckpoint(t *testing.T) {
	e := newEnv(t)
	m := e.partialBatch()
	cr := e.submit(m, models.JobKindUp)

	if _, err := e.svc.Reject(ctx, e.leaderB, cr.ID, "tunggu jam sepi"); err != nil {
		t.Fatal(err)
	}
	if got := e.migrationStatus(m.ID); got != models.MigrationPartial {
		t.Errorf("status migration setelah resume ditolak = %s, mau tetap partial", got)
	}
	if len(e.q.calls) != 0 {
		t.Error("penolakan tidak boleh mengirim ke queue")
	}
}

func TestApprove_DispatchFailureKeepsApprovalAndCanRetry(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationDraft, "DROP TABLE x", false)
	cr := e.submit(m, models.JobKindUp)
	queueDown := errors.New("queue down")
	e.q.err = queueDown

	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Approve(ctx, e.leaderB, cr.ID, "")
	if !errors.Is(err, ErrDispatchFailed) || !errors.Is(err, queueDown) {
		t.Fatalf("error = %v", err)
	}
	if got := e.request(cr.ID); got.Status != models.ChangeRequestApproved || got.JobID != nil {
		t.Fatalf("persetujuan harus tetap tercatat tanpa job: %+v", got)
	}

	e.q.err = nil
	got, err := e.svc.Dispatch(ctx, cr.ID)
	if err != nil || got.JobID == nil {
		t.Fatalf("dispatch ulang: %v, %+v", err, got)
	}
	if again, err := e.svc.Dispatch(ctx, cr.ID); err != nil || *again.JobID != *got.JobID || len(e.q.calls) != 1 {
		t.Errorf("dispatch harus idempoten: %v, calls=%d", err, len(e.q.calls))
	}
}

func TestDispatch_RequiresApproval(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)
	if _, err := e.svc.Dispatch(ctx, cr.ID); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("error = %v", err)
	}
}

func TestRollback_SubmitAndApprove(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationSuccess, "DROP TABLE x", false)
	exec := uuid.NewString()
	e.execution(m, exec, 1, models.JobKindUp, models.ExecutionSuccess, nil)

	cr := e.submit(m, models.JobKindRollback)

	if cr.RollbackOf == nil || *cr.RollbackOf != exec {
		t.Fatalf("rollback_of = %v, mau %s", cr.RollbackOf, exec)
	}
	if e.migrationStatus(m.ID) != models.MigrationSuccess {
		t.Error("rollback tidak boleh mengubah status migration")
	}
	e.approveAll(cr)

	if len(e.q.calls) != 1 {
		t.Fatalf("queue dipanggil %d kali", len(e.q.calls))
	}
	if call := e.q.calls[0]; call.Kind != models.JobKindRollback || *call.RollbackOf != exec {
		t.Errorf("panggilan queue = %+v", call)
	}
}

func TestRollback_Rejections(t *testing.T) {
	e := newEnv(t)
	run := func(m *models.Migration) {
		e.execution(m, uuid.NewString(), 1, models.JobKindUp, models.ExecutionSuccess, nil)
	}

	never := e.migration(models.MigrationSuccess, "x", false)
	irreversible := e.migration(models.MigrationSuccess, "", true)
	run(irreversible)
	first := e.migration(models.MigrationSuccess, "x", false)
	run(first)
	second := e.migration(models.MigrationSuccess, "x", false)
	run(second)

	cases := []struct {
		name string
		mig  *models.Migration
		want error
	}{
		{"belum pernah dieksekusi", never, workflow.ErrNotActive},
		{"irreversible", irreversible, workflow.ErrIrreversible},
		{"bukan yang aktif terakhir", first, workflow.ErrNotLastActive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := e.svc.Submit(ctx, e.editor, e.connID, c.mig.ID, models.JobKindRollback); !errors.Is(err, c.want) {
				t.Fatalf("error = %v, mau %v", err, c.want)
			}
		})
	}
	if _, err := e.svc.Submit(ctx, e.editor, e.connID, second.ID, models.JobKindRollback); err != nil {
		t.Errorf("yang aktif terakhir harus boleh: %v", err)
	}
}

func TestRollback_AttemptsCountAsOneExecution(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationSuccess, "DROP TABLE x", false)
	exec := uuid.NewString()
	e.execution(m, exec, 1, models.JobKindUp, models.ExecutionFailed, nil)
	e.execution(m, exec, 2, models.JobKindUp, models.ExecutionSuccess, nil)

	cr := e.submit(m, models.JobKindRollback)

	if *cr.RollbackOf != exec {
		t.Errorf("rollback_of = %s, mau %s", *cr.RollbackOf, exec)
	}
}

func TestDispatch_StaleRollbackIsRefused(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationSuccess, "DROP TABLE x", false)
	exec := uuid.NewString()
	e.execution(m, exec, 1, models.JobKindUp, models.ExecutionSuccess, nil)
	cr := e.submit(m, models.JobKindRollback)
	e.q.err = errors.New("queue down")
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Approve(ctx, e.leaderB, cr.ID, ""); !errors.Is(err, ErrDispatchFailed) {
		t.Fatalf("approve terakhir: %v", err)
	}

	// Rollback lain sudah membatalkan eksekusi yang sama sebelum dispatch ulang.
	e.execution(m, uuid.NewString(), 1, models.JobKindRollback, models.ExecutionSuccess, &exec)
	e.q.err = nil

	if _, err := e.svc.Dispatch(ctx, cr.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("error = %v", err)
	}
	if len(e.q.calls) != 0 {
		t.Error("permintaan basi tidak boleh masuk queue")
	}
}

func TestForward_AfterRollbackIsQueued(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationSuccess, "DROP TABLE x", false)
	exec := uuid.NewString()
	e.execution(m, exec, 1, models.JobKindUp, models.ExecutionSuccess, nil)
	e.execution(m, uuid.NewString(), 1, models.JobKindRollback, models.ExecutionSuccess, &exec)

	cr := e.submit(m, models.JobKindForward)

	if cr.RollbackOf != nil {
		t.Errorf("forward tidak boleh punya rollback_of: %v", *cr.RollbackOf)
	}
	e.approveAll(cr)
	if len(e.q.calls) != 1 || e.q.calls[0].Kind != models.JobKindForward {
		t.Fatalf("panggilan queue = %+v", e.q.calls)
	}
}

func TestForward_AfterFailedAttemptIsAllowed(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationFailed, "DROP TABLE x", false)
	e.execution(m, uuid.NewString(), 1, models.JobKindUp, models.ExecutionFailed, nil)

	e.submit(m, models.JobKindForward)
}

func TestForward_Rejections(t *testing.T) {
	e := newEnv(t)
	run := func(m *models.Migration) string {
		id := uuid.NewString()
		e.execution(m, id, 1, models.JobKindUp, models.ExecutionSuccess, nil)
		return id
	}
	undo := func(m *models.Migration, of string) {
		e.execution(m, uuid.NewString(), 1, models.JobKindRollback, models.ExecutionSuccess, &of)
	}

	never := e.migration(models.MigrationDraft, "x", false)
	active := e.migration(models.MigrationSuccess, "x", false)
	run(active)
	first := e.migration(models.MigrationSuccess, "x", false)
	second := e.migration(models.MigrationSuccess, "x", false)
	firstExec, secondExec := run(first), run(second)
	undo(second, secondExec)
	undo(first, firstExec)

	cases := []struct {
		name string
		mig  *models.Migration
		want error
	}{
		{"belum pernah dieksekusi", never, workflow.ErrNeverExecuted},
		{"masih aktif", active, workflow.ErrAlreadyActive},
		{"melompati urutan", second, workflow.ErrOutOfOrder},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := e.svc.Submit(ctx, e.editor, e.connID, c.mig.ID, models.JobKindForward); !errors.Is(err, c.want) {
				t.Fatalf("error = %v, mau %v", err, c.want)
			}
		})
	}
	if _, err := e.svc.Submit(ctx, e.editor, e.connID, first.ID, models.JobKindForward); err != nil {
		t.Errorf("migration pertama harus boleh di-forward: %v", err)
	}
}

func TestDispatch_StaleForwardIsRefused(t *testing.T) {
	e := newEnv(t)
	a := e.migration(models.MigrationSuccess, "x", false)
	b := e.migration(models.MigrationSuccess, "x", false)
	aExec, bExec := uuid.NewString(), uuid.NewString()
	e.execution(a, aExec, 1, models.JobKindUp, models.ExecutionSuccess, nil)
	e.execution(b, bExec, 1, models.JobKindUp, models.ExecutionSuccess, nil)
	e.execution(b, uuid.NewString(), 1, models.JobKindRollback, models.ExecutionSuccess, &bExec)
	cr := e.submit(b, models.JobKindForward)
	e.q.err = errors.New("queue down")
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Approve(ctx, e.leaderB, cr.ID, ""); !errors.Is(err, ErrDispatchFailed) {
		t.Fatalf("approve terakhir: %v", err)
	}

	// A dibatalkan sebelum dispatch ulang, sehingga forward B melompati urutan.
	e.execution(a, uuid.NewString(), 1, models.JobKindRollback, models.ExecutionSuccess, &aExec)
	e.q.err = nil

	if _, err := e.svc.Dispatch(ctx, cr.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("error = %v", err)
	}
	if len(e.q.calls) != 0 {
		t.Error("permintaan basi tidak boleh masuk queue")
	}
}

// Contoh riwayat di spesifikasi: A [E1], B [E2], B rollback [E3], B forward [E4], C [E5].
func TestHistory_HideRollback(t *testing.T) {
	e := newEnv(t)
	a := e.migration(models.MigrationSuccess, "x", false)
	b := e.migration(models.MigrationSuccess, "x", false)
	c := e.migration(models.MigrationSuccess, "x", false)
	e1, e2, e3, e4, e5 := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	e.execution(a, e1, 1, models.JobKindUp, models.ExecutionSuccess, nil)
	e.execution(b, e2, 1, models.JobKindUp, models.ExecutionSuccess, nil)
	e.execution(b, e3, 1, models.JobKindRollback, models.ExecutionSuccess, &e2)
	e.execution(b, e4, 1, models.JobKindForward, models.ExecutionSuccess, nil)
	e.execution(c, e5, 1, models.JobKindUp, models.ExecutionFailed, nil)
	e.execution(c, e5, 2, models.JobKindUp, models.ExecutionSuccess, nil)

	ids := func(hide bool) []string {
		rows, err := e.svc.History(ctx, e.connID, hide)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rows {
			got = append(got, fmt.Sprintf("%s#%d", r.ExecutionID, r.Attempt))
		}
		return got
	}
	want := func(items ...string) []string { return items }

	if got, full := ids(false), want(e1+"#1", e2+"#1", e3+"#1", e4+"#1", e5+"#1", e5+"#2"); fmt.Sprint(got) != fmt.Sprint(full) {
		t.Errorf("riwayat lengkap = %v, mau %v", got, full)
	}
	if got, hidden := ids(true), want(e1+"#1", e4+"#1", e5+"#1", e5+"#2"); fmt.Sprint(got) != fmt.Sprint(hidden) {
		t.Errorf("hide rollback = %v, mau %v (E2 dan E3 disembunyikan, attempt gagal tetap tampil)", got, hidden)
	}
}

func TestSubmit_DeletedLeaderCountsAsNoLeader(t *testing.T) {
	e := newEnv(t)
	if err := e.db.Delete(&models.User{}, "id = ?", e.leaderB).Error; err != nil {
		t.Fatal(err)
	}
	m := e.migration(models.MigrationDraft, "DROP TABLE x", false)

	if _, err := e.svc.Submit(ctx, e.editor, e.connID, m.ID, models.JobKindUp); !errors.Is(err, ErrGroupsWithoutLeader) {
		t.Fatalf("error = %v", err)
	}
	if got := e.migrationStatus(m.ID); got != models.MigrationDraft {
		t.Errorf("status harus tetap draft, dapat %s", got)
	}
}

func TestApprove_LeaderOfBothGroupsCanReapproveAfterLeaderChange(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}

	// Leader A kini memimpin kedua group; tidak ada keputusan baru yang memicu evaluasi ulang selain approve ulang ini.
	if err := e.db.Model(&models.Group{}).Where("name = ?", "B").Update("leader_id", e.leaderA).Error; err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Approve(ctx, e.leaderA, cr.ID, "")
	if err != nil {
		t.Fatalf("approve ulang: %v", err)
	}
	if got.Status != models.ChangeRequestApproved || got.JobID == nil || len(e.q.calls) != 1 {
		t.Errorf("setelah approve ulang: %+v, calls=%d", got, len(e.q.calls))
	}
	if e.migrationStatus(cr.MigrationID) != models.MigrationApproved {
		t.Errorf("status migration = %s, mau approved", e.migrationStatus(cr.MigrationID))
	}
}

func TestApprove_ReapproveWithoutLeaderChangeStaysPending(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Reject(ctx, e.leaderA, cr.ID, ""); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("reject setelah approve: %v", err)
	}
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); !errors.Is(err, ErrAlreadyDecided) {
		t.Errorf("approve ulang tanpa perubahan: %v", err)
	}
	if got := e.request(cr.ID); got.Status != models.ChangeRequestPendingReview || len(e.q.calls) != 0 {
		t.Errorf("harus tetap menunggu review: %+v, calls=%d", got, len(e.q.calls))
	}
}

func TestEnrich_ShowsApprovalProgress(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationDraft, "DROP TABLE x", false)
	cr := e.submit(m, models.JobKindUp)
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, "lgtm"); err != nil {
		t.Fatal(err)
	}

	forB := e.request(cr.ID)
	if err := e.svc.Enrich(ctx, e.leaderB, &forB); err != nil {
		t.Fatal(err)
	}
	if forB.MigrationName != m.Name || forB.ConnectionName != "c1" || forB.RequestedByName != "editor" {
		t.Errorf("nama turunan = %q / %q / %q", forB.MigrationName, forB.ConnectionName, forB.RequestedByName)
	}
	if len(forB.Reviewers) != 2 {
		t.Fatalf("reviewers = %+v", forB.Reviewers)
	}
	byGroup := make(map[string]models.Reviewer)
	for _, r := range forB.Reviewers {
		byGroup[r.GroupName] = r
	}
	if a := byGroup["A"]; a.Decision != "approve" || a.Comment != "lgtm" || a.DecidedAt == nil || a.LeaderName == nil || *a.LeaderName != "leader-a" {
		t.Errorf("group A = %+v", a)
	}
	if b := byGroup["B"]; b.Decision != models.ReviewerPending || b.DecidedAt != nil {
		t.Errorf("group B = %+v", b)
	}
	if !forB.CanDecide {
		t.Error("leader B belum memutuskan, can_decide harus true")
	}

	for name, user := range map[string]string{"leader A (sudah setuju)": e.leaderA, "editor": e.editor, "outside": e.outside, "anonim": ""} {
		got := e.request(cr.ID)
		if err := e.svc.Enrich(ctx, user, &got); err != nil {
			t.Fatal(err)
		}
		if got.CanDecide {
			t.Errorf("%s: can_decide harus false", name)
		}
	}

	e.approveRest(cr)
	done := e.request(cr.ID)
	if err := e.svc.Enrich(ctx, e.leaderB, &done); err != nil {
		t.Fatal(err)
	}
	if done.CanDecide || len(done.Reviewers) != 2 {
		t.Errorf("setelah diputuskan: can_decide=%v reviewers=%+v", done.CanDecide, done.Reviewers)
	}
	for _, r := range done.Reviewers {
		if r.Decision != "approve" || r.LeaderName == nil {
			t.Errorf("keputusan tercatat = %+v", r)
		}
	}
}

func TestEnrich_RejectedRequestListsRecordedDecisionOnly(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)
	if _, err := e.svc.Reject(ctx, e.leaderB, cr.ID, "terlalu berisiko"); err != nil {
		t.Fatal(err)
	}

	got := e.request(cr.ID)
	if err := e.svc.Enrich(ctx, e.leaderA, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Reviewers) != 1 || got.Reviewers[0].Decision != "reject" || got.Reviewers[0].Comment != "terlalu berisiko" {
		t.Errorf("reviewers = %+v", got.Reviewers)
	}
	if got.CanDecide {
		t.Error("permintaan yang sudah ditolak tidak bisa diputuskan lagi")
	}
}

func TestPendingFor_ListsOnlyUndecidedRequestsOfLeader(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)

	pending := func(user string) []models.ChangeRequest {
		t.Helper()
		got, err := e.svc.PendingFor(ctx, user, 50)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := pending(e.leaderA); len(got) != 1 || got[0].ID != cr.ID {
		t.Fatalf("leader A = %+v", got)
	}
	if got := pending(e.leaderB); len(got) != 1 {
		t.Fatalf("leader B = %+v", got)
	}
	for name, user := range map[string]string{"editor": e.editor, "viewer": e.viewer, "outside": e.outside, "bukan-uuid": "u1"} {
		if got := pending(user); len(got) != 0 {
			t.Errorf("%s bukan leader: %+v", name, got)
		}
	}

	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := pending(e.leaderA); len(got) != 0 {
		t.Errorf("leader A sudah memutuskan: %+v", got)
	}
	if got := pending(e.leaderB); len(got) != 1 {
		t.Errorf("leader B masih harus memutuskan: %+v", got)
	}

	e.approveRest(cr)
	if got := pending(e.leaderB); len(got) != 0 {
		t.Errorf("permintaan sudah selesai: %+v", got)
	}
}

func TestRedispatchApproved_SendsRequestsThatNeverReachedTheQueue(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)
	e.q.err = errors.New("queue down")
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Approve(ctx, e.leaderB, cr.ID, ""); !errors.Is(err, ErrDispatchFailed) {
		t.Fatalf("error = %v", err)
	}

	if n, err := e.svc.RedispatchApproved(ctx); err != nil || n != 0 {
		t.Fatalf("queue masih mati: n=%d err=%v", n, err)
	}
	if got := e.request(cr.ID); got.JobID != nil {
		t.Fatalf("job tidak boleh terisi: %+v", got)
	}

	e.q.err = nil
	if n, err := e.svc.RedispatchApproved(ctx); err != nil || n != 1 {
		t.Fatalf("setelah queue pulih: n=%d err=%v", n, err)
	}
	if got := e.request(cr.ID); got.JobID == nil || len(e.q.calls) != 1 {
		t.Errorf("job harus terisi: %+v calls=%d", got, len(e.q.calls))
	}
	if n, err := e.svc.RedispatchApproved(ctx); err != nil || n != 0 || len(e.q.calls) != 1 {
		t.Errorf("tidak ada lagi yang perlu dikirim: n=%d err=%v calls=%d", n, err, len(e.q.calls))
	}
}

// approveRest menyetujui sebagai leader B (leader A diasumsikan sudah memutuskan).
func (e *env) approveRest(cr *models.ChangeRequest) {
	e.t.Helper()
	if _, err := e.svc.Approve(ctx, e.leaderB, cr.ID, ""); err != nil {
		e.t.Fatalf("approve leader B: %v", err)
	}
}

// approveWhileQueueIsDown menyetujui sebagai kedua leader saat queue mati: permintaan tersimpan sebagai approved tanpa job.
func (e *env) approveWhileQueueIsDown(cr *models.ChangeRequest) {
	e.t.Helper()
	e.q.err = errors.New("queue down")
	if _, err := e.svc.Approve(ctx, e.leaderA, cr.ID, ""); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Approve(ctx, e.leaderB, cr.ID, ""); !errors.Is(err, ErrDispatchFailed) {
		e.t.Fatalf("approve terakhir: %v", err)
	}
	e.q.err = nil
}

func TestRollback_DuplicateWhileInFlightIsBlocked(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationSuccess, "DROP TABLE x", false)
	e.execution(m, uuid.NewString(), 1, models.JobKindUp, models.ExecutionSuccess, nil)
	submit := func() error {
		_, err := e.svc.Submit(ctx, e.editor, e.connID, m.ID, models.JobKindRollback)
		return err
	}
	cr := e.submit(m, models.JobKindRollback)

	if err := submit(); !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("menunggu review: %v", err)
	}

	e.approveWhileQueueIsDown(cr)
	if err := submit(); !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("disetujui tapi belum masuk queue: %v", err)
	}

	got, err := e.svc.Dispatch(ctx, cr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := submit(); !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("job masih berjalan: %v", err)
	}

	// Job gagal tidak mengubah apa pun, jadi pengajuan baru boleh.
	if err := e.db.Model(&models.MigrationJob{}).Where("id = ?", *got.JobID).
		Update("status", models.JobFailed).Error; err != nil {
		t.Fatal(err)
	}
	if err := submit(); err != nil {
		t.Errorf("setelah job selesai: %v", err)
	}
}

func TestForward_DuplicateWhileInFlightIsBlocked(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationSuccess, "DROP TABLE x", false)
	up := uuid.NewString()
	e.execution(m, up, 1, models.JobKindUp, models.ExecutionSuccess, nil)
	e.execution(m, uuid.NewString(), 1, models.JobKindRollback, models.ExecutionSuccess, &up)
	cr := e.submit(m, models.JobKindForward)
	e.approveWhileQueueIsDown(cr)

	if _, err := e.svc.Submit(ctx, e.editor, e.connID, m.ID, models.JobKindForward); !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("error = %v", err)
	}
}

func TestDispatch_ReusesJobCreatedBeforeLinkWasSaved(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)
	e.approveWhileQueueIsDown(cr)

	// Enqueue sebelumnya sebenarnya berhasil, tetapi job_id belum sempat tersimpan.
	jobID, err := e.q.enqueue(ctx, e.request(cr.ID))
	if err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.Dispatch(ctx, cr.ID)

	if err != nil || got.JobID == nil || *got.JobID != jobID {
		t.Fatalf("dispatch = %+v, %v; mau job %s", got, err, jobID)
	}
	if len(e.q.calls) != 1 {
		t.Errorf("queue dipanggil %d kali, mau 1 (tanpa job ganda)", len(e.q.calls))
	}
}

func TestDispatch_LinksFinishedJobInsteadOfReportingStale(t *testing.T) {
	e := newEnv(t)
	m := e.migration(models.MigrationSuccess, "DROP TABLE x", false)
	e.execution(m, uuid.NewString(), 1, models.JobKindUp, models.ExecutionSuccess, nil)
	cr := e.submit(m, models.JobKindRollback)
	e.approveWhileQueueIsDown(cr)

	// Job sempat dibuat dan sudah selesai (rollback tercatat), tetapi job_id belum tersimpan.
	jobID, err := e.q.enqueue(ctx, e.request(cr.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.Model(&models.MigrationJob{}).Where("id = ?", jobID).
		Update("status", models.JobSuccess).Error; err != nil {
		t.Fatal(err)
	}
	e.execution(m, uuid.NewString(), 1, models.JobKindRollback, models.ExecutionSuccess, e.request(cr.ID).RollbackOf)

	got, err := e.svc.Dispatch(ctx, cr.ID)

	if err != nil || got.JobID == nil || *got.JobID != jobID {
		t.Fatalf("dispatch = %+v, %v; mau job %s", got, err, jobID)
	}
}

func TestDispatch_ConcurrentCallsEnqueueOnce(t *testing.T) {
	e := newEnv(t)
	cr := e.submit(e.migration(models.MigrationDraft, "DROP TABLE x", false), models.JobKindUp)
	e.approveWhileQueueIsDown(cr)

	const callers = 4
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := e.svc.Dispatch(ctx, cr.ID)
			errs[i] = err
			if got != nil && got.JobID != nil {
				ids[i] = *got.JobID
			}
		}()
	}
	wg.Wait()

	for i := range ids {
		if errs[i] != nil || ids[i] == "" || ids[i] != ids[0] {
			t.Fatalf("pemanggil %d: id=%q err=%v (id pertama %q)", i, ids[i], errs[i], ids[0])
		}
	}
	if len(e.q.calls) != 1 {
		t.Errorf("queue dipanggil %d kali, mau 1", len(e.q.calls))
	}
}
