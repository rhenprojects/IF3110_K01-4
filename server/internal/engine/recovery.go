package engine

import (
	"context"
	"time"
	"web_backend/internal/models"
	"web_backend/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func (e *Engine) reaperLoop(ctx context.Context) {
	every := e.opts.StaleAfter / 3
	if every < time.Second {
		every = time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := e.Recover(ctx); err != nil && ctx.Err() == nil {
			e.log.Error("recover jobs failed", "event", "job.recover_failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Recover menyelesaikan job yang worker-nya berhenti di tengah jalan (status running tanpa heartbeat) dan job uncertain
func (e *Engine) Recover(ctx context.Context) (int, error) {
	cutoff := e.now().Add(-e.opts.StaleAfter)
	var jobs []models.MigrationJob
	if err := e.db.WithContext(ctx).
		Where("(status = ? AND (heartbeat_at IS NULL OR heartbeat_at < ?)) OR status = ?",
			models.JobRunning, cutoff, models.JobUncertain).
		Order("created_at").Find(&jobs).Error; err != nil {
		return 0, err
	}
	resolved := 0
	for i := range jobs {
		ok, err := e.resolve(ctx, jobs[i].ID, cutoff)
		if err != nil {
			return resolved, err
		}
		if ok {
			resolved++
		}
	}
	return resolved, nil
}

func (e *Engine) resolve(ctx context.Context, jobID string, cutoff time.Time) (bool, error) {
	var job models.MigrationJob
	if err := e.db.WithContext(ctx).First(&job, "id = ?", jobID).Error; err != nil {
		return false, err
	}
	// Lock masih dipegang berarti worker pemiliknya masih hidup
	release, ok, err := e.locker.TryLock(ctx, job.ConnectionID)
	if err != nil || !ok {
		return false, err
	}
	defer release()

	// Periksa ulang setelah lock didapat; worker bisa saja baru selesai.
	if err := e.db.WithContext(ctx).First(&job, "id = ?", jobID).Error; err != nil {
		return false, err
	}
	stale := job.Status == models.JobRunning && (job.HeartbeatAt == nil || job.HeartbeatAt.Before(cutoff))
	if !stale && job.Status != models.JobUncertain {
		return false, nil
	}

	// Pemulihan dicatat sebagai bagian dari trace job aslinya.
	ctx, span := tracer.Start(telemetry.Extract(ctx, job.TraceParent), "job.recover",
		trace.WithAttributes(append(jobAttrs(&job), attribute.String("job.previous_status", string(job.Status)))...))
	defer span.End()

	spec := specForJob(&job)
	logger := e.attemptLogger(spec)
	pool, err := e.pools.Pool(ctx, job.ConnectionID)
	committed := false
	if err == nil {
		committed, err = ledgerHas(ctx, pool, job.ExecutionID)
	}
	if err != nil {
		if job.Status != models.JobUncertain {
			e.db.WithContext(ctx).Model(&models.MigrationJob{}).
				Where("id = ? AND status = ?", job.ID, models.JobRunning).
				Updates(map[string]any{"status": models.JobUncertain,
					"error": "worker stopped; commit result could not be verified: " + err.Error(), "updated_at": e.now()})
		}
		span.SetAttributes(attribute.String("job.status", string(models.JobUncertain)))
		logger.WarnContext(ctx, "job result still uncertain", "event", "job.uncertain", "status", models.JobUncertain, "error", err.Error())
		return false, nil
	}

	var m models.Migration
	if err := e.db.WithContext(ctx).First(&m, "id = ?", job.MigrationID).Error; err != nil {
		return false,err
	}
	started := e.now()
	if job.StartedAt != nil 
	{
		started = *job.StartedAt
	}
	res := attemptResult{sql: sqlFor(&m, job.Kind), started: started, finished: e.now(), recovered: true}
	if committed 
	{
		
		res.status = models.ExecutionSuccess
	} else {
		res.status = models.ExecutionFailed
		res.errMsg = "worker stopped before the transaction was committed"
		res.transient = true
	}
	next, err := e.finishJob(ctx, &job, &m, res)
	if err != nil {
		span.RecordError(err)
		return false, err
	}
	span.SetAttributes(attribute.Bool("ledger.committed", committed), attribute.String("job.status", string(next)))
	logger.InfoContext(ctx, "interrupted job recovered", "event", "job.recovered", "committed", committed, "status", next)
	return true, nil
}
