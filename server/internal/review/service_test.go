package review

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	calls []models.ChangeRequest
	err   error
}

func (q *fakeQueue) enqueue(_ context.Context, cr models.ChangeRequest) (string, error) {
	if q.err != nil {
		return "", q.err
	}
	q.calls = append(q.calls, cr)
	return uuid.NewString(), nil
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

	q := &fakeQueue{}
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
