package engine

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
	"web_backend/internal/models"
	"web_backend/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// claimSQL mengambil satu job yang siap dijalankan
const claimSQL = `
UPDATE migration_jobs
SET status = 'running', attempts = attempts + 1, worker_id = ?, heartbeat_at = now(),
    started_at = COALESCE(started_at, now()), updated_at = now()
WHERE id = (
	SELECT j.id FROM migration_jobs j
	WHERE j.status = 'queued' AND j.run_after <= now()
	  AND NOT EXISTS (
		SELECT 1 FROM migration_jobs r
		WHERE r.connection_id = j.connection_id AND r.status IN ('running', 'uncertain'))
	  AND NOT EXISTS (
		SELECT 1 FROM migrations pm
		WHERE pm.connection_id = j.connection_id AND pm.status = 'partial'
		  AND pm.batch_status <> 'reverted' AND pm.id <> j.migration_id)
	ORDER BY j.created_at, j.id
	LIMIT 1
	FOR UPDATE SKIP LOCKED)
RETURNING *`

// start menyalakan worker pool dan reaper
// stop menunggu job yang sedang berjalan selesai dicatat sebelum kembali.
func (e *Engine) Start(workers int) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	host, _ := os.Hostname()
	for i := 0; i < workers; i++ {
		id := fmt.Sprintf("%s-%d-w%d", host, os.Getpid(), i+1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.workerLoop(ctx, id)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.reaperLoop(ctx)
	}()
	e.log.Info("migration workers started", "event", "worker.started", "workers", workers)
	return func() {
		cancel()
		wg.Wait()
	}
}

func (e *Engine) workerLoop(ctx context.Context, workerID string) {
	for ctx.Err() == nil {
		job, err := e.claim(ctx, workerID)
		if err != nil {
			if ctx.Err() == nil {
				e.log.Error("claim job failed", "event", "job.claim_failed", "worker", workerID, "error", err.Error())
			}
			e.idle(ctx)
			continue
		}
		if job == nil {
			e.idle(ctx)
			continue
		}
		e.process(job, workerID)
	}
}

func (e *Engine) idle(ctx context.Context) {
	t := time.NewTimer(e.opts.PollEvery)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-e.wake:
	case <-t.C:
	}
}

func (e *Engine) claim(ctx context.Context, workerID string) (*models.MigrationJob, error) {
	var job models.MigrationJob
	if err := e.db.WithContext(ctx).Raw(claimSQL, workerID).Scan(&job).Error; err != nil {
		return nil, err
	}
	if job.ID == "" {
		return nil, nil
	}
	return &job, nil
}

// process memakai context tanpa pembatalan
func (e *Engine) process(job *models.MigrationJob, workerID string) {
	ctx, span := tracer.Start(telemetry.Extract(context.Background(), job.TraceParent), "job.process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(append(jobAttrs(job), attribute.String("worker.id", workerID))...))
	defer span.End()

	spec := specForJob(job)
	logger := e.attemptLogger(spec).With("worker", workerID)
	queueWait := time.Since(job.RunAfter)
	logger.InfoContext(ctx, "job claimed", "event", "job.claimed", "status", models.JobRunning, "queue_wait_ms", queueWait.Milliseconds())

	lockCtx, lockSpan := tracer.Start(ctx, "lock.acquire", trace.WithAttributes(attribute.String("db.connection_id", job.ConnectionID)))
	release, ok, err := e.locker.TryLock(lockCtx, job.ConnectionID)
	lockSpan.SetAttributes(attribute.Bool("lock.acquired", ok))
	endSpan(lockSpan, err)
	if err != nil || !ok {
		e.db.WithContext(ctx).Model(&models.MigrationJob{}).
			Where("id = ? AND worker_id = ? AND status = ?", job.ID, workerID, models.JobRunning).
			Updates(map[string]any{
				"status": models.JobQueued, "attempts": gorm.Expr("attempts - 1"), "worker_id": "",
				"heartbeat_at": nil, "run_after": e.now().Add(2 * e.opts.PollEvery), "updated_at": e.now(),
			})
		reason := "busy"
		if err != nil {

			reason = err.Error()

		}
		span.SetAttributes(attribute.String("job.outcome", "requeued_lock_busy"))
		logger.InfoContext(ctx, "database lock not acquired, job requeued", "event", "lock.busy", "reason", reason)
		return
	}
	defer release()
	logger.InfoContext(ctx, "database lock acquired", "event", "lock.acquired")

	stopHeartbeat := e.heartbeat(job.ID, workerID)
	m, res, err := e.execute(ctx, spec)
	stopHeartbeat()
	if err != nil {

		span.RecordError(err)

		span.SetStatus(codes.Error, "attempt aborted")
		logger.ErrorContext(ctx, "attempt aborted", "event", "job.aborted", "error", err.Error())
		return
	}
	next, err := e.finishJob(ctx, job, m, res)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "record attempt failed")
		logger.ErrorContext(ctx, "record attempt failed", "event", "job.record_failed", "error", err.Error())
		return
	}
	span.SetAttributes(attribute.String("job.status", string(next)), attribute.String("execution.status", string(res.status)))
	if next == models.JobFailed || next == models.JobUncertain {
		span.SetStatus(codes.Error, res.errMsg)
	}
	logger.InfoContext(ctx, "job attempt finished", "event", "job.finished", "status", next,
		"execution_status", res.status, "duration_ms", res.durationMs())
}

func (e *Engine) heartbeat(jobID, workerID string) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(e.opts.HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				e.db.Model(&models.MigrationJob{}).
					Where("id = ? AND worker_id = ? AND status = ?", jobID, workerID, models.JobRunning).
					Update("heartbeat_at", e.now())
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
	}
}
