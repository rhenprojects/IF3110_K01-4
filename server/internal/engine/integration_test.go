package engine_test

// Integration test engine dengan PostgreSQL asli (TEST_PG_DSN)
// Database yang sama dipakai sebagai database platform dan database target

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"web_backend/internal/engine"
	"web_backend/internal/models"
	"web_backend/internal/targetdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type stubPools struct {
	pools map[string]*pgxpool.Pool
}

func (s *stubPools) Pool(_ context.Context, connectionID string) (*pgxpool.Pool, error) {
	if p, ok := s.pools[connectionID]; ok {
		return p, nil
	}
	return nil, targetdb.ErrNotFound
}

type fixture struct {
	t      *testing.T
	db     *gorm.DB
	dsn    string
	schema string
	pools  *stubPools
	target *pgxpool.Pool
	userID string
	connID string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Seluruh tabel platform dihapus lalu dibuat ulang lewat models
	all := models.All()
	for i := len(all) - 1; i >= 0; i-- {
		if err := db.Migrator().DropTable(all[i]); err != nil {
			t.Fatalf("drop table %T: %v", all[i], err)
		}
	}
	if err := models.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	target, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(target.Close)

	f := &fixture{t: t, db: db, dsn: dsn, target: target, pools: &stubPools{pools: map[string]*pgxpool.Pool{}},
		schema: fmt.Sprintf("engine_test_%d", time.Now().UnixNano())}
	f.execTarget(`CREATE SCHEMA ` + f.s())
	t.Cleanup(func() {
		_, _ = target.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+f.s()+` CASCADE`)
	})

	user := models.User{Username: "engine-user", Name: "Engine User", Password: "x"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	f.userID = user.ID
	f.connID = f.addConnection("target", target)
	return f
}

// s mengembalikan nama schema fixture yang sudah di-quote.
func (f *fixture) s() string { return pgx.Identifier{f.schema}.Sanitize() }

func (f *fixture) addConnection(name string, pool *pgxpool.Pool) string {
	f.t.Helper()
	conn := models.DBConnection{Name: name, Host: "stub", Port: 5432, DBName: "stub", Username: "stub",
		PasswordEnc: []byte{1}, SSLMode: "disable"}
	if err := f.db.Create(&conn).Error; err != nil {
		f.t.Fatal(err)
	}
	f.pools.pools[conn.ID] = pool
	return conn.ID
}

func (f *fixture) engine() *engine.Engine {
	f.t.Helper()
	eng, err := engine.New(f.db, f.pools, engine.Options{
		PollEvery: 20 * time.Millisecond, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond,
		HeartbeatEvery: 100 * time.Millisecond, StaleAfter: time.Second,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return eng
}

func (f *fixture) migration(connID, name, up, down string) models.Migration {
	f.t.Helper()
	m := models.Migration{ConnectionID: connID, Name: name, SQL: up, SQLDown: down, Revision: 1,
		Status: models.MigrationApproved, CreatedBy: f.userID}
	if err := f.db.Create(&m).Error; err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) enqueue(eng *engine.Engine, connID, migrationID string, kind models.JobKind, rollbackOf string) *models.MigrationJob {
	f.t.Helper()
	job, err := eng.Enqueue(context.Background(), engine.EnqueueRequest{
		ConnectionID: connID, MigrationID: migrationID, Kind: kind, RollbackOf: rollbackOf, RequestedBy: f.userID,
	})
	if err != nil {
		f.t.Fatalf("enqueue %s: %v", kind, err)
	}
	return job
}

func (f *fixture) waitJob(jobID string) models.MigrationJob {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var job models.MigrationJob
		if err := f.db.First(&job, "id = ?", jobID).Error; err != nil {
			f.t.Fatal(err)
		}
		if job.Status == models.JobSuccess || job.Status == models.JobFailed {
			return job
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("job %s did not finish, last status %s (error %q)", jobID, job.Status, job.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *fixture) execTarget(statements ...string) {
	f.t.Helper()
	for _, stmt := range statements {
		if _, err := f.target.Exec(context.Background(), stmt); err != nil {
			f.t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

func (f *fixture) tableExists(table string) bool {
	f.t.Helper()
	var exists bool
	if err := f.target.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`,
		pgx.Identifier{f.schema, table}.Sanitize()).Scan(&exists); err != nil {
		f.t.Fatal(err)
	}
	return exists
}

func (f *fixture) executions(migrationID string) []models.MigrationExecution {
	f.t.Helper()
	var rows []models.MigrationExecution
	if err := f.db.Where("migration_id = ?", migrationID).Order("id").Find(&rows).Error; err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func (f *fixture) reloadMigration(id string) models.Migration {
	f.t.Helper()
	var m models.Migration
	if err := f.db.First(&m, "id = ?", id).Error; err != nil {
		f.t.Fatal(err)
	}
	return m
}

func TestIntegrationQueuedMigrationRunsAndIsRecorded(t *testing.T) {
	f := setup(t)
	eng := f.engine()
	t.Cleanup(eng.Start(2))

	m := f.migration(f.connID, "create items", `CREATE TABLE `+f.s()+`.items (id int)`, `DROP TABLE `+f.s()+`.items`)
	job := f.enqueue(eng, f.connID, m.ID, models.JobKindUp, "")
	if job.Status != models.JobQueued || job.ExecutionID == "" {
		t.Fatalf("enqueued job = %+v", job)
	}

	done := f.waitJob(job.ID)
	if done.Status != models.JobSuccess || done.Attempts != 1 {
		t.Fatalf("job = %+v", done)
	}
	if !f.tableExists("items") {
		t.Fatal("items table was not created")
	}
	got := f.reloadMigration(m.ID)
	if got.Status != models.MigrationSuccess || got.Version == nil || *got.Version != 1 || got.ExecutedBy == nil {
		t.Errorf("migration = %+v", got)
	}
	execs := f.executions(m.ID)
	if len(execs) != 1 {
		t.Fatalf("executions = %+v", execs)
	}
	x := execs[0]
	if x.ExecutionID != job.ExecutionID || x.Attempt != 1 || x.Status != models.ExecutionSuccess || x.SQL != m.SQL ||
		x.JobID == nil || *x.JobID != job.ID || x.ExecutedBy != f.userID || x.Revision != 1 {
		t.Errorf("execution record = %+v", x)
	}
}

func TestIntegrationFailedSQLIsRolledBackAndNotRetried(t *testing.T) {
	f := setup(t)
	eng := f.engine()
	t.Cleanup(eng.Start(1))

	m := f.migration(f.connID, "half broken",
		`CREATE TABLE `+f.s()+`.half (id int); INSERT INTO `+f.s()+`.missing VALUES (1)`, `SELECT 1`)
	done := f.waitJob(f.enqueue(eng, f.connID, m.ID, models.JobKindUp, "").ID)
	if done.Status != models.JobFailed || done.Attempts != 1 {
		t.Fatalf("job = %+v", done)
	}
	if f.tableExists("half") {
		t.Fatal("first statement was committed although the second failed")
	}
	execs := f.executions(m.ID)
	if len(execs) != 1 || execs[0].Status != models.ExecutionFailed || execs[0].SQLState != "42P01" {
		t.Errorf("executions = %+v", execs)
	}
	if got := f.reloadMigration(m.ID); got.Status != models.MigrationFailed || got.Version != nil {
		t.Errorf("migration = %+v", got)
	}
}

func TestIntegrationTransientFailureIsRetriedThenFails(t *testing.T) {
	f := setup(t)
	unreachable, err := pgxpool.New(context.Background(), "postgres://x:x@127.0.0.1:1/x?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unreachable.Close)
	downID := f.addConnection("down", unreachable)

	eng := f.engine()
	t.Cleanup(eng.Start(1))
	m := f.migration(downID, "never runs", `CREATE TABLE t (id int)`, `DROP TABLE t`)
	done := f.waitJob(f.enqueue(eng, downID, m.ID, models.JobKindUp, "").ID)

	if done.Status != models.JobFailed || done.Attempts != done.MaxAttempts || done.MaxAttempts != 3 {
		t.Fatalf("job = %+v", done)
	}
	execs := f.executions(m.ID)
	if len(execs) != 3 {
		t.Fatalf("want 3 recorded attempts, got %+v", execs)
	}
	for i, x := range execs {
		if x.Attempt != i+1 || x.Status != models.ExecutionFailed || x.ExecutionID != done.ExecutionID {
			t.Errorf("attempt %d = %+v", i+1, x)
		}
	}
	if got := f.reloadMigration(m.ID); got.Status != models.MigrationFailed {
		t.Errorf("migration after exhausted retries = %s", got.Status)
	}
}

func TestIntegrationRedeliveredJobDoesNotCommitTwice(t *testing.T) {
	f := setup(t)
	f.execTarget(`CREATE TABLE ` + f.s() + `.counter (n int)`)
	eng := f.engine()
	t.Cleanup(eng.Start(1))

	m := f.migration(f.connID, "insert once", `INSERT INTO `+f.s()+`.counter VALUES (1)`, `DELETE FROM `+f.s()+`.counter`)
	job := f.enqueue(eng, f.connID, m.ID, models.JobKindUp, "")
	f.waitJob(job.ID)

	// Simulasi pesan duplikat: job yang sama masuk queue lagi dengan execution ID yang sama.
	if err := f.db.Model(&models.MigrationJob{}).Where("id = ?", job.ID).
		Updates(map[string]any{"status": models.JobQueued, "run_after": time.Now().Add(-time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	eng.Wake()
	f.waitJob(job.ID)

	var rows int
	if err := f.target.QueryRow(context.Background(), `SELECT count(*) FROM `+f.s()+`.counter`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("counter has %d rows: duplicate delivery committed twice", rows)
	}
	execs := f.executions(m.ID)
	if len(execs) != 2 || !execs[1].Recovered || execs[1].Status != models.ExecutionSuccess {
		t.Errorf("second attempt should be recorded as already committed: %+v", execs)
	}
}

func TestIntegrationRecoverStaleJobUsesLedger(t *testing.T) {
	f := setup(t)
	eng := f.engine()
	ctx := context.Background()
	stale := time.Now().Add(-time.Hour)

	staleJob := func(m models.Migration) models.MigrationJob {
		job := models.MigrationJob{ConnectionID: f.connID, MigrationID: m.ID, ExecutionID: uuid.NewString(),
			Kind: models.JobKindUp, Status: models.JobRunning, Attempts: 1, MaxAttempts: 3, RunAfter: stale,
			WorkerID: "dead-worker", HeartbeatAt: &stale, StartedAt: &stale, RequestedBy: f.userID}
		if err := f.db.Create(&job).Error; err != nil {
			t.Fatal(err)
		}
		return job
	}

	// Worker mati SETELAH commit: perubahan dan bukti eksekusi sudah ada di target.
	committed := f.migration(f.connID, "committed", `CREATE TABLE `+f.s()+`.done_before_crash (id int)`, `SELECT 1`)
	jobA := staleJob(committed)
	f.execTarget(committed.SQL,
		`CREATE SCHEMA IF NOT EXISTS _dbmp`,
		`CREATE TABLE IF NOT EXISTS _dbmp.executions (execution_id uuid PRIMARY KEY, migration_id uuid NOT NULL, kind text NOT NULL, committed_at timestamptz NOT NULL DEFAULT now())`,
		fmt.Sprintf(`INSERT INTO _dbmp.executions (execution_id, migration_id, kind) VALUES ('%s', '%s', 'up')`, jobA.ExecutionID, committed.ID))

	// Worker mati SEBELUM commit: tidak ada bukti eksekusi.
	notCommitted := f.migration(f.connID, "not committed", `CREATE TABLE `+f.s()+`.retried_after_crash (id int)`, `SELECT 1`)
	jobB := staleJob(notCommitted)

	resolved, err := eng.Recover(ctx)
	if err != nil || resolved != 2 {
		t.Fatalf("Recover = %d, %v", resolved, err)
	}

	var gotA models.MigrationJob
	f.db.First(&gotA, "id = ?", jobA.ID)
	if gotA.Status != models.JobSuccess {
		t.Errorf("committed job status = %s, want success", gotA.Status)
	}
	if execs := f.executions(committed.ID); len(execs) != 1 || !execs[0].Recovered || execs[0].Status != models.ExecutionSuccess {
		t.Errorf("committed job history = %+v", execs)
	}
	if m := f.reloadMigration(committed.ID); m.Status != models.MigrationSuccess {
		t.Errorf("committed migration status = %s", m.Status)
	}

	var gotB models.MigrationJob
	f.db.First(&gotB, "id = ?", jobB.ID)
	if gotB.Status != models.JobQueued || gotB.Attempts != 1 {
		t.Fatalf("uncommitted job should be requeued for retry: %+v", gotB)
	}
	if f.tableExists("retried_after_crash") {
		t.Fatal("uncommitted job changed the target before retry")
	}

	// Worker baru mengambil alih dan menyelesaikan job yang belum ter-commit.
	t.Cleanup(eng.Start(1))
	if done := f.waitJob(jobB.ID); done.Status != models.JobSuccess || done.Attempts != 2 {
		t.Errorf("retried job = %+v", done)
	}
	if !f.tableExists("retried_after_crash") {
		t.Error("retried job did not apply the migration")
	}
	if execs := f.executions(notCommitted.ID); len(execs) != 2 || execs[0].Status != models.ExecutionFailed || execs[1].Status != models.ExecutionSuccess {
		t.Errorf("uncommitted job history = %+v", execs)
	}
}

func TestIntegrationLockIsExclusiveAcrossProcessesAndReleasedOnCrash(t *testing.T) {
	f := setup(t)
	open := func() *engine.Locker {
		db, err := gorm.Open(postgres.Open(f.dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		return engine.NewLocker(sqlDB)
	}
	ctx := context.Background()
	procA, procB := open(), open() // dua "proses" dengan pool koneksi terpisah

	releaseA, ok, err := procA.TryLock(ctx, f.connID)
	if err != nil || !ok {
		t.Fatalf("process A lock: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := procB.TryLock(ctx, f.connID); ok {
		t.Fatal("process B acquired a lock held by process A")
	}
	if release, ok, _ := procB.TryLock(ctx, uuid.NewString()); !ok {
		t.Fatal("a different database should not be blocked")
	} else {
		release()
	}

	// Simulasi worker crash: sesi pemegang lock diputus paksa
	if _, err := f.target.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND classid = 17474 AND granted AND pid <> pg_backend_pid()`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		release, ok, _ := procB.TryLock(ctx, f.connID)
		if ok {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lock was not released after the holder's session died")
		}
		time.Sleep(50 * time.Millisecond)
	}
	releaseA() // unlock gagal karena sesi sudah mati; koneksinya harus dibuang tanpa panic
}

func TestIntegrationOneMigrationPerDatabaseAndParallelAcrossDatabases(t *testing.T) {
	f := setup(t)
	f.execTarget(`CREATE TABLE ` + f.s() + `.spans (db text, job text, phase text, at timestamptz)`)
	otherID := f.addConnection("same server, other database", f.target)

	span := func(db, job string) string {
		ins := `INSERT INTO ` + f.s() + `.spans VALUES ('` + db + `', '` + job + `', '%s', clock_timestamp())`
		return fmt.Sprintf(ins, "start") + `; SELECT pg_sleep(0.4); ` + fmt.Sprintf(ins, "end")
	}

	// Dua engine = dua proses worker, masing-masing dengan 2 worker
	engA, engB := f.engine(), f.engine()
	var jobs []string
	for i := 1; i <= 3; i++ {
		m := f.migration(f.connID, fmt.Sprintf("a%d", i), span("A", fmt.Sprintf("a%d", i)), `SELECT 1`)
		jobs = append(jobs, f.enqueue(engA, f.connID, m.ID, models.JobKindUp, "").ID)
	}
	m := f.migration(otherID, "b1", span("B", "b1"), `SELECT 1`)
	jobs = append(jobs, f.enqueue(engB, otherID, m.ID, models.JobKindUp, "").ID)
	t.Cleanup(engA.Start(2))
	t.Cleanup(engB.Start(2))
	for _, id := range jobs {
		if done := f.waitJob(id); done.Status != models.JobSuccess {
			t.Fatalf("job %s = %+v", id, done)
		}
	}

	type span2 struct {
		job        string
		start, end time.Time
	}
	read := func(db string) []span2 {
		rows, err := f.target.Query(context.Background(),
			`SELECT job, min(at) FILTER (WHERE phase='start'), max(at) FILTER (WHERE phase='end')
			 FROM `+f.s()+`.spans WHERE db = $1 GROUP BY job`, db)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []span2
		for rows.Next() {
			var s span2
			if err := rows.Scan(&s.job, &s.start, &s.end); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
		return out
	}

	a := read("A")
	if len(a) != 3 {
		t.Fatalf("database A spans = %+v", a)
	}
	for i := 1; i < len(a); i++ {
		if a[i].start.Before(a[i-1].end) {
			t.Errorf("migrations on the same database overlapped: %+v and %+v", a[i-1], a[i])
		}
	}
	b := read("B")
	overlap := false
	for _, s := range a {
		if b[0].start.Before(s.end) && s.start.Before(b[0].end) {
			overlap = true
		}
	}
	if !overlap {
		t.Errorf("different databases did not run in parallel: A=%+v B=%+v", a, b)
	}
}

func TestIntegrationRollbackAndForwardUseOriginalSQL(t *testing.T) {
	f := setup(t)
	eng := f.engine()
	t.Cleanup(eng.Start(1))

	m := f.migration(f.connID, "reversible", `CREATE TABLE `+f.s()+`.rb (id int)`, `DROP TABLE `+f.s()+`.rb`)
	up := f.waitJob(f.enqueue(eng, f.connID, m.ID, models.JobKindUp, "").ID)
	if !f.tableExists("rb") {
		t.Fatal("up did not run")
	}

	rollback := f.waitJob(f.enqueue(eng, f.connID, m.ID, models.JobKindRollback, up.ExecutionID).ID)
	if rollback.Status != models.JobSuccess || f.tableExists("rb") {
		t.Fatalf("rollback = %+v, table still exists: %v", rollback, f.tableExists("rb"))
	}
	forward := f.waitJob(f.enqueue(eng, f.connID, m.ID, models.JobKindForward, "").ID)
	if forward.Status != models.JobSuccess || !f.tableExists("rb") {
		t.Fatalf("forward = %+v", forward)
	}

	execs := f.executions(m.ID)
	if len(execs) != 3 {
		t.Fatalf("executions = %+v", execs)
	}
	kinds := []models.JobKind{execs[0].Kind, execs[1].Kind, execs[2].Kind}
	if kinds[0] != models.JobKindUp || kinds[1] != models.JobKindRollback || kinds[2] != models.JobKindForward {
		t.Errorf("kinds = %v", kinds)
	}
	if execs[1].SQL != m.SQLDown || execs[1].RollbackOf == nil || *execs[1].RollbackOf != up.ExecutionID {
		t.Errorf("rollback record = %+v", execs[1])
	}
	if execs[2].SQL != m.SQL || execs[2].ExecutionID == up.ExecutionID {
		t.Errorf("forward must be a new execution with the original up SQL: %+v", execs[2])
	}
}

func TestIntegrationForwardRetriesFailedMigration(t *testing.T) {
	f := setup(t)
	eng := f.engine()
	t.Cleanup(eng.Start(1))

	m := f.migration(f.connID, "late table", `INSERT INTO `+f.s()+`.late VALUES (1)`, `SELECT 1`)
	failed := f.waitJob(f.enqueue(eng, f.connID, m.ID, models.JobKindUp, "").ID)
	if failed.Status != models.JobFailed {
		t.Fatalf("up = %+v", failed)
	}

	f.execTarget(`CREATE TABLE ` + f.s() + `.late (id int)`)
	forward := f.waitJob(f.enqueue(eng, f.connID, m.ID, models.JobKindForward, "").ID)
	if forward.Status != models.JobSuccess {
		t.Fatalf("forward = %+v", forward)
	}
	if got := f.reloadMigration(m.ID); got.Status != models.MigrationSuccess || got.Version == nil || *got.Version != 1 || got.Error != "" {
		t.Errorf("migration = %+v", got)
	}
	execs := f.executions(m.ID)
	if len(execs) != 2 || execs[1].Kind != models.JobKindForward || execs[1].MigrationVersion == nil || *execs[1].MigrationVersion != 1 {
		t.Errorf("executions = %+v", execs)
	}
}

func TestIntegrationEnqueueRejectsInvalidRequests(t *testing.T) {
	f := setup(t)
	eng := f.engine()
	ctx := context.Background()
	req := func(migrationID string, kind models.JobKind, rollbackOf string) error {
		_, err := eng.Enqueue(ctx, engine.EnqueueRequest{ConnectionID: f.connID, MigrationID: migrationID,
			Kind: kind, RollbackOf: rollbackOf, RequestedBy: f.userID})
		return err
	}

	noDown := f.migration(f.connID, "no down", `SELECT 1`, ``)
	var verr *engine.ValidationError
	if err := req(noDown.ID, models.JobKindUp, ""); !errors.As(err, &verr) || verr.Field != "sql_down" {
		t.Errorf("missing down: %v", err)
	}

	irreversible := models.Migration{ConnectionID: f.connID, Name: "wipe", SQL: `SELECT 1`, Irreversible: true,
		IrreversibleReason: "data loss", Revision: 1, Status: models.MigrationSuccess, CreatedBy: f.userID}
	f.db.Create(&irreversible)
	if err := req(irreversible.ID, models.JobKindRollback, uuid.NewString()); !errors.Is(err, engine.ErrIrreversible) {
		t.Errorf("rollback irreversible: %v", err)
	}

	ok := f.migration(f.connID, "ok", `SELECT 1`, `SELECT 1`)
	if err := req(ok.ID, models.JobKindRollback, uuid.NewString()); !errors.Is(err, engine.ErrRollbackTarget) {
		t.Errorf("rollback unknown execution: %v", err)
	}
	if err := req(ok.ID, models.JobKindForward, ""); !errors.Is(err, engine.ErrNotExecutable) {
		t.Errorf("forward before up: %v", err)
	}
	for _, status := range []models.MigrationStatus{models.MigrationDraft, models.MigrationPendingReview} {
		unapproved := models.Migration{ConnectionID: f.connID, Name: string(status), SQL: `SELECT 1`, SQLDown: `SELECT 1`,
			Revision: 1, Status: status, CreatedBy: f.userID}
		f.db.Create(&unapproved)
		if err := req(unapproved.ID, models.JobKindUp, ""); !errors.Is(err, engine.ErrNotExecutable) {
			t.Errorf("up while %s: %v", status, err)
		}
	}
	if err := req(ok.ID, models.JobKind("sideways"), ""); !errors.Is(err, engine.ErrInvalidKind) {
		t.Errorf("unknown kind: %v", err)
	}
	if err := req(uuid.NewString(), models.JobKindUp, ""); !errors.Is(err, engine.ErrMigrationNotFound) {
		t.Errorf("unknown migration: %v", err)
	}
	if err := req(ok.ID, models.JobKindUp, ""); err != nil {
		t.Fatalf("valid enqueue: %v", err)
	}
	if err := req(ok.ID, models.JobKindUp, ""); !errors.Is(err, engine.ErrJobActive) {
		t.Errorf("second active job: %v", err)
	}
}

func TestIntegrationHistoryIsAppendOnly(t *testing.T) {
	f := setup(t)
	eng := f.engine()
	t.Cleanup(eng.Start(1))
	m := f.migration(f.connID, "history", `SELECT 1`, `SELECT 1`)
	f.waitJob(f.enqueue(eng, f.connID, m.ID, models.JobKindUp, "").ID)

	if err := f.db.Exec(`UPDATE migration_executions SET status = 'failed'`).Error; err == nil {
		t.Error("UPDATE on migration_executions should be rejected")
	}
	if err := f.db.Exec(`DELETE FROM migration_executions`).Error; err == nil {
		t.Error("DELETE on migration_executions should be rejected")
	}
	if len(f.executions(m.ID)) != 1 {
		t.Error("history row was modified")
	}
}

// Tracer engine terikat ke TracerProvider global pertama yang dipasang, jadi recorder dipasang sekali untuk seluruh test di paket ini.
var (
	recorderOnce sync.Once
	recorder     *tracetest.SpanRecorder
)

func spanRecorder() *tracetest.SpanRecorder {
	recorderOnce.Do(func() {
		recorder = tracetest.NewSpanRecorder()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
		otel.SetTextMapPropagator(propagation.TraceContext{})
	})
	return recorder
}

func TestIntegrationTraceFollowsJobFromRequestToWorker(t *testing.T) {
	rec := spanRecorder()
	f := setup(t)
	eng := f.engine()
	t.Cleanup(eng.Start(1))

	// Span "request" mewakili request API yang memanggil Enqueue
	ctx, root := otel.Tracer("test").Start(context.Background(), "request")
	m := f.migration(f.connID, "traced", `CREATE TABLE `+f.s()+`.traced (id int)`, `DROP TABLE `+f.s()+`.traced`)
	job, err := eng.Enqueue(ctx, engine.EnqueueRequest{ConnectionID: f.connID, MigrationID: m.ID, Kind: models.JobKindUp, RequestedBy: f.userID})
	root.End()
	if err != nil {
		t.Fatal(err)
	}
	traceID := root.SpanContext().TraceID()
	if !strings.Contains(job.TraceParent, traceID.String()) {
		t.Fatalf("job traceparent %q does not carry trace %s", job.TraceParent, traceID)
	}
	f.waitJob(job.ID)

	// job.process ditutup sesaat setelah status job tersimpan
	want := []string{"queue.enqueue", "job.process", "lock.acquire", "migration.execute", "db.query", "db.commit", "history.record"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		byName := map[string]sdktrace.ReadOnlySpan{}
		for _, s := range rec.Ended() {
			if s.SpanContext().TraceID() == traceID {
				byName[s.Name()] = s
			}
		}
		missing := []string{}
		for _, name := range want {
			if _, ok := byName[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			if byName["job.process"].Parent().SpanID() != byName["queue.enqueue"].SpanContext().SpanID() {
				t.Error("job.process should continue from queue.enqueue")
			}
			if byName["db.query"].Parent().SpanID() != byName["migration.execute"].SpanContext().SpanID() {
				t.Error("db.query should be a child of migration.execute")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("spans missing from trace %s: %v", traceID, missing)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
