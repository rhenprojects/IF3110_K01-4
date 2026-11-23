package engine

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"
	"web_backend/internal/models"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

var (
	errAlreadyFinalized = errors.New("job was already finalized")
	ErrUncertain        = errors.New("commit result could not be verified")
)

type attemptSpec struct {
	jobID        *string
	executionID  string
	attempt      int
	connectionID string
	migrationID  string
	kind         models.JobKind
	executedBy   string
}

type attemptResult struct {
	status    models.ExecutionStatus
	sql       string
	started   time.Time
	finished  time.Time
	errMsg    string
	sqlState  string
	transient bool
	uncertain bool // commit gagal dan bukti eksekusi tidak bisa diperiksa
	recovered bool // hasil dipastikan lewat ledger, bukan dari eksekusi attempt ini
}

func (r attemptResult) durationMs() int64 {
	ms := r.finished.Sub(r.started).Milliseconds()
	if ms < 1 && !r.recovered {
		ms = 1
	}
	if ms < 0 {
		ms = 0
	}
	return ms
}

// sqlFor memilih SQL asli yang immutable: up untuk eksekusi/forward, down untuk rollback.
func sqlFor(m *models.Migration, kind models.JobKind) string {
	if kind == models.JobKindRollback {
		return m.SQLDown
	}
	return m.SQL
}

func (e *Engine) attemptLogger(spec attemptSpec) *slog.Logger {
	l := e.log.With("execution_id", spec.executionID, "attempt", spec.attempt,
		"migration_id", spec.migrationID, "database", spec.connectionID, "kind", spec.kind, "actor", spec.executedBy)
	if spec.jobID != nil {
		l = l.With("job_id", *spec.jobID)
	}
	return l
}

// execute menjalankan satu attempt di database target. Kegagalan di database target dikembalikan sebagai attemptResult;
func (e *Engine) execute(ctx context.Context, spec attemptSpec) (_ *models.Migration, res attemptResult, err error) {
	ctx, span := tracer.Start(ctx, "migration.execute", trace.WithAttributes(specAttrs(spec)...))
	defer func() {
		span.SetAttributes(attribute.String("execution.status", string(res.status)),
			attribute.Bool("execution.recovered", res.recovered), attribute.Bool("execution.uncertain", res.uncertain))
		if err == nil && res.errMsg != "" {
			span.SetStatus(codes.Error, res.errMsg)
		}
		endSpan(span, err)
	}()

	var m models.Migration
	if err := e.db.WithContext(ctx).
		First(&m, "id = ? AND connection_id = ?", spec.migrationID, spec.connectionID).Error; err != nil {
		return nil, attemptResult{}, err
	}
	logger := e.attemptLogger(spec)
	res = attemptResult{sql: sqlFor(&m, spec.kind), started: e.now()}

	fail := func(err error, phase string) (*models.Migration, attemptResult, error) {
		res.finished = e.now()
		res.status = models.ExecutionFailed
		res.errMsg = err.Error()
		res.sqlState, res.transient = classify(err)
		span.SetAttributes(attribute.String("execution.phase", phase), attribute.String("db.response.status_code", res.sqlState),
			attribute.Bool("error.transient", res.transient))
		logger.WarnContext(ctx, "migration attempt failed", "event", "sql.failed", "phase", phase,
			"status", res.status, "error", res.errMsg, "sqlstate", res.sqlState, "transient", res.transient,
			"duration_ms", res.durationMs())
		return &m, res, nil
	}

	pool, err := e.pools.Pool(ctx, spec.connectionID)
	if err != nil {
		return fail(err, "connect")
	}
	if err := ensureLedger(ctx, pool); err != nil {
		return fail(err, "ledger")
	}
	committed, err := ledgerHas(ctx, pool, spec.executionID)
	if err != nil {
		return fail(err, "ledger")
	}
	if committed {
		res.status, res.finished, res.recovered = models.ExecutionSuccess, e.now(), true
		logger.InfoContext(ctx, "execution already committed", "event", "sql.already_committed", "status", res.status)
		return &m, res, nil
	}

	logger.InfoContext(ctx, "migration SQL started", "event", "sql.start", "query", redactSQL(res.sql))
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fail(err, "begin")
	}
	queryCtx, querySpan := tracer.Start(ctx, "db.query", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system.name", "postgresql"), attribute.String("db.query.text", queryText(res.sql))))
	_, err = tx.Exec(queryCtx, res.sql)
	endSpan(querySpan, err)
	if err != nil {
		_ = tx.Rollback(context.Background())
		logger.InfoContext(ctx, "transaction rolled back", "event", "tx.rollback")
		return fail(err, "execute")
	}
	if err := ledgerRecord(ctx, tx, spec.executionID, m.ID, string(spec.kind)); err != nil {
		_ = tx.Rollback(context.Background())
		logger.InfoContext(ctx, "transaction rolled back", "event", "tx.rollback")
		return fail(err, "ledger")
	}
	commitCtx, commitSpan := tracer.Start(ctx, "db.commit", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system.name", "postgresql")))
	err = tx.Commit(commitCtx)
	endSpan(commitSpan, err)
	if err != nil {
		checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ok, verr := ledgerHas(checkCtx, pool, spec.executionID)
		switch {
		case verr != nil:
			res.uncertain, res.finished = true, e.now()
			res.errMsg = "commit result unknown: " + err.Error()
			logger.ErrorContext(ctx, "commit result could not be verified", "event", "tx.commit_uncertain",
				"error", res.errMsg)
			return &m, res, nil
		case !ok:
			return fail(err, "commit")
		}
	}
	res.status, res.finished = models.ExecutionSuccess, e.now()
	logger.InfoContext(ctx, "transaction committed", "event", "tx.commit", "status", res.status,
		"duration_ms", res.durationMs())
	return &m, res, nil
}

// applyToMigration memperbarui baris migration sesuai hasil akhir eksekusi up atau forward, lalu mengembalikan versi migration untuk dicatat di riwayat.
// Forward yang gagal dan rollback tidak mengubah baris migration.
func (e *Engine) applyToMigration(tx *gorm.DB, m *models.Migration, kind models.JobKind, actor string, res attemptResult, final bool) (*int, error) {
	skip := kind == models.JobKindRollback ||
		(kind == models.JobKindForward && res.status != models.ExecutionSuccess) ||
		(!final && res.status != models.ExecutionSuccess)
	if m == nil || skip {
		if m == nil {
			return nil, nil
		}
		return m.Version, nil
	}
	duration := res.durationMs()
	updates := map[string]any{
		"executed_by": actor,
		"executed_at": res.finished,
		"duration_ms": duration,
		"updated_at":  e.now(),
	}
	if res.status == models.ExecutionSuccess {
		version := m.Version
		if version == nil {
			var max sql.NullInt64
			if err := tx.Raw(`SELECT MAX(version) FROM migrations WHERE connection_id = ? AND status = ?`,
				m.ConnectionID, models.MigrationSuccess).Scan(&max).Error; err != nil {
				return nil, err
			}
			next := 1
			if max.Valid {
				next = int(max.Int64) + 1
			}
			version = &next
		}
		updates["status"] = models.MigrationSuccess
		updates["version"] = *version
		updates["error"] = ""
		if err := tx.Model(&models.Migration{}).Where("id = ?", m.ID).Updates(updates).Error; err != nil {
			return nil, err
		}
		return version, nil
	}
	updates["status"] = models.MigrationFailed
	updates["error"] = res.errMsg
	updates["version"] = nil
	if err := tx.Model(&models.Migration{}).Where("id = ?", m.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	return nil, nil
}

func newExecutionRecord(spec attemptSpec, m *models.Migration, res attemptResult, version *int, rollbackOf *string) models.MigrationExecution {
	revision := 1
	if m != nil {
		revision = m.Revision
	}
	return models.MigrationExecution{
		ExecutionID:      spec.executionID,
		Attempt:          spec.attempt,
		JobID:            spec.jobID,
		ConnectionID:     spec.connectionID,
		MigrationID:      spec.migrationID,
		Kind:             spec.kind,
		Status:           res.status,
		Revision:         revision,
		MigrationVersion: version,
		SQL:              res.sql,
		RollbackOf:       rollbackOf,
		ExecutedBy:       spec.executedBy,
		StartedAt:        res.started,
		FinishedAt:       res.finished,
		DurationMs:       res.durationMs(),
		Error:            res.errMsg,
		SQLState:         res.sqlState,
		Recovered:        res.recovered,
	}
}

func specForJob(job *models.MigrationJob) attemptSpec {
	jobID := job.ID
	return attemptSpec{
		jobID:        &jobID,
		executionID:  job.ExecutionID,
		attempt:      job.Attempts,
		connectionID: job.ConnectionID,
		migrationID:  job.MigrationID,
		kind:         job.Kind,
		executedBy:   job.RequestedBy,
	}
}

// finishJob mencatat attempt ke riwayat dan memindahkan job ke status berikutnya dalam satu transaksi database platform.
func (e *Engine) finishJob(ctx context.Context, job *models.MigrationJob, m *models.Migration, res attemptResult) (next models.JobStatus, err error) {
	ctx, span := tracer.Start(ctx, "history.record", trace.WithAttributes(jobAttrs(job)...))
	defer func() {
		span.SetAttributes(attribute.String("job.status", string(next)))
		endSpan(span, err)
	}()
	now := e.now()
	active := []models.JobStatus{models.JobRunning, models.JobUncertain}
	spec := specForJob(job)

	if res.uncertain {
		err = e.db.WithContext(ctx).Model(&models.MigrationJob{}).
			Where("id = ? AND status IN ?", job.ID, active).
			Updates(map[string]any{"status": models.JobUncertain, "error": res.errMsg, "updated_at": now}).Error
		return models.JobUncertain, err
	}

	next = models.JobFailed
	switch {
	case res.status == models.ExecutionSuccess:
		next = models.JobSuccess
	case res.transient && job.Attempts < job.MaxAttempts:
		next = models.JobQueued
	}

	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		version, err := e.applyToMigration(tx, m, job.Kind, job.RequestedBy, res, next != models.JobQueued)
		if err != nil {
			return err
		}
		record := newExecutionRecord(spec, m, res, version, job.RollbackOf)
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		updates := map[string]any{"status": next, "error": res.errMsg, "worker_id": "", "heartbeat_at": nil, "updated_at": now}
		switch next {
		case models.JobQueued:
			updates["run_after"] = now.Add(backoff(job.Attempts, e.opts.BackoffBase, e.opts.BackoffMax))
		default:
			updates["finished_at"] = now
		}
		r := tx.Model(&models.MigrationJob{}).Where("id = ? AND status IN ?", job.ID, active).Updates(updates)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return errAlreadyFinalized
		}
		return nil
	})
	return next, err
}

// Outcome adalah hasil RunLocked untuk pemanggil di luar queue.
type Outcome struct {
	Status     models.ExecutionStatus
	DurationMs int64
	Error      string
}

// RunLocked menjalankan satu eksekusi up secara langsung untuk pemanggil yang sudah memegang lock database ini
func (e *Engine) RunLocked(ctx context.Context, connectionID, migrationID, executedBy, executionID string, attempt int) (Outcome, error) {
	spec := attemptSpec{
		executionID: executionID, attempt: attempt, connectionID: connectionID,
		migrationID: migrationID, kind: models.JobKindUp, executedBy: executedBy,
	}
	m, res, err := e.execute(ctx, spec)
	if err != nil {
		return Outcome{}, err
	}
	if res.uncertain {
		return Outcome{}, ErrUncertain
	}
	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		version, err := e.applyToMigration(tx, m, models.JobKindUp, executedBy, res, true)
		if err != nil {
			return err
		}
		record := newExecutionRecord(spec, m, res, version, nil)
		return tx.Create(&record).Error
	})
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: res.status, DurationMs: res.durationMs(), Error: res.errMsg}, nil
}
