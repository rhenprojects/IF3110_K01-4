package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"web_backend/internal/models"
	"web_backend/internal/observability"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	batchSchema = `CREATE SCHEMA IF NOT EXISTS _dbmp`
	batchRuns   = `CREATE TABLE IF NOT EXISTS _dbmp.batch_runs (
		run_id uuid PRIMARY KEY,
		execution_id uuid NOT NULL,
		migration_id uuid NOT NULL,
		status text NOT NULL,
		cursor_id bigint NOT NULL DEFAULT 0,
		committed_rows bigint NOT NULL DEFAULT 0,
		committed_chunks bigint NOT NULL DEFAULT 0,
		updated_at timestamptz NOT NULL DEFAULT now()
	)`
	batchChunks = `CREATE TABLE IF NOT EXISTS _dbmp.batch_chunks (
		run_id uuid NOT NULL,
		chunk_no bigint NOT NULL,
		execution_id uuid NOT NULL,
		from_id bigint NOT NULL,
		to_id bigint NOT NULL,
		rows bigint NOT NULL,
		down_sql text NOT NULL,
		committed_at timestamptz NOT NULL DEFAULT now(),
		PRIMARY KEY (run_id, chunk_no)
	)`
)

type batchRun struct {
	Status string
	Cursor int64
	Rows   int64
	Chunks int64
}

func ensureBatchTables(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range []string{batchSchema, batchRuns, batchChunks} {
		if _, err := pool.Exec(ctx, stmt); err != nil && !isDuplicateObject(err) {
			return err
		}
	}
	return nil
}

func loadRun(ctx context.Context, pool *pgxpool.Pool, runID string) (batchRun, bool, error) {
	var run batchRun
	if runID == "" {
		return run, false, nil
	}
	err := pool.QueryRow(ctx,
		`SELECT status, cursor_id, committed_rows, committed_chunks FROM _dbmp.batch_runs WHERE run_id = $1`,
		runID).Scan(&run.Status, &run.Cursor, &run.Rows, &run.Chunks)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, false, nil
	}
	return run, true, err
}

func createRun(ctx context.Context, pool *pgxpool.Pool, runID, executionID, migrationID string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO _dbmp.batch_runs (run_id, execution_id, migration_id, status)
		 VALUES ($1,$2,$3,'running') ON CONFLICT (run_id) DO NOTHING`,
		runID, executionID, migrationID)
	return err
}

func markRun(ctx context.Context, pool *pgxpool.Pool, runID, status string) error {
	_, err := pool.Exec(ctx,
		`UPDATE _dbmp.batch_runs SET status = $2, updated_at = now() WHERE run_id = $1`, runID, status)
	return err
}

// chunkSQL mengganti placeholder angka dan memastikan query mengembalikan id.
func chunkSQL(template string, batchSize int, lastID int64) string {
	sql := strings.TrimRight(strings.TrimSpace(template), "; \t\r\n")
	sql = strings.ReplaceAll(sql, ":last_id", strconv.FormatInt(lastID, 10))
	sql = strings.ReplaceAll(sql, ":batch_size", strconv.Itoa(batchSize))
	if !strings.Contains(strings.ToUpper(sql), "RETURNING") {
		sql += " RETURNING id"
	}
	return sql
}

func downSQL(template string, fromID, toID int64) string {
	sql := strings.TrimRight(strings.TrimSpace(template), "; \t\r\n")
	sql = strings.ReplaceAll(sql, ":from_id", strconv.FormatInt(fromID, 10))
	sql = strings.ReplaceAll(sql, ":to_id", strconv.FormatInt(toID, 10))
	return sql
}

func setStatementTimeout(ctx context.Context, tx pgx.Tx, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	_, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds()))
	return err
}

func (e *Engine) batchCancelled(ctx context.Context, migrationID string) bool {
	var cancelled bool
	if err := e.db.WithContext(ctx).Model(&models.Migration{}).
		Select("batch_cancel_requested").Where("id = ?", migrationID).Scan(&cancelled).Error; err != nil {
		return false
	}
	return cancelled
}

// executeChunked menjalankan satu migration besar per potongan row. Checkpoint
// target (run, chunk, cursor) ditulis dalam transaction yang sama dengan data.
func (e *Engine) executeChunked(ctx context.Context, spec attemptSpec, m *models.Migration, pool *pgxpool.Pool) attemptResult {
	res := attemptResult{sql: m.SQL, started: e.now()}
	var cursor, rows, chunks int64
	fail := func(err error, phase string) attemptResult {
		res.finished = e.now()
		res.status = models.ExecutionFailed
		res.errMsg = err.Error()
		res.batchStatus = "partial"
		res.batchCursor, res.batchRows, res.batchChunks = cursor, rows, chunks
		observability.ObserveBatch("partial", res.finished.Sub(res.started))
		e.log.WarnContext(ctx, "batch chunk failed", "event", "batch.failed", "phase", phase,
			"migration_id", m.ID, "execution_id", spec.executionID, "error", err.Error())
		return res
	}
	if m.SQLDown == "" || m.BatchSize <= 0 {
		return fail(errors.New("invalid batch definition"), "validate")
	}
	if err := ensureBatchTables(ctx, pool); err != nil {
		return fail(err, "checkpoint")
	}

	runID := ""
	if m.BatchRunID != nil {
		runID = *m.BatchRunID
	}
	run, exists, err := loadRun(ctx, pool, runID)
	if err != nil {
		return fail(err, "checkpoint")
	}
	if !exists || run.Status == "success" || run.Status == "reverted" {
		runID = uuid.NewString()
		run = batchRun{Status: "running"}
		if err := createRun(ctx, pool, runID, spec.executionID, m.ID); err != nil {
			return fail(err, "checkpoint")
		}
		if err := e.db.WithContext(ctx).Model(&models.Migration{}).Where("id = ?", m.ID).Updates(map[string]any{
			"batch_run_id": runID, "batch_cursor": 0, "batch_rows": 0, "batch_chunks": 0,
			"batch_status": "running", "batch_cancel_requested": false,
		}).Error; err != nil {
			return fail(err, "mirror")
		}
		m.BatchRunID = &runID
	}

	cursor, rows, chunks = run.Cursor, run.Rows, run.Chunks
	for {
		chunkStarted := e.now()
		if e.batchCancelled(ctx, m.ID) {
			return e.revertBatch(ctx, spec, m, pool, runID)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fail(err, "begin")
		}
		if err := setStatementTimeout(ctx, tx, e.opts.StatementTimeout); err != nil {
			_ = tx.Rollback(context.Background())
			return fail(err, "timeout")
		}
		rowsResult, err := tx.Query(ctx, chunkSQL(m.SQL, m.BatchSize, cursor))
		if err != nil {
			_ = tx.Rollback(context.Background())
			return fail(err, "execute")
		}
		var n, minID, maxID int64
		first := true
		for rowsResult.Next() {
			values, err := rowsResult.Values()
			if err != nil || len(values) == 0 {
				rowsResult.Close()
				_ = tx.Rollback(context.Background())
				return fail(err, "scan")
			}
			id, ok := values[0].(int64)
			if !ok {
				rowsResult.Close()
				_ = tx.Rollback(context.Background())
				return fail(errors.New("batch SQL must return bigint id"), "scan")
			}
			n++
			if first || id < minID {
				minID = id
			}
			if first || id > maxID {
				maxID = id
			}
			first = false
		}
		if err := rowsResult.Err(); err != nil {
			rowsResult.Close()
			_ = tx.Rollback(context.Background())
			return fail(err, "scan")
		}
		rowsResult.Close()
		if n == 0 {
			_ = tx.Rollback(context.Background())
			_ = markRun(ctx, pool, runID, "success")
			observability.ObserveBatch("success", e.now().Sub(res.started))
			res.status = models.ExecutionSuccess
			res.batchStatus = "success"
			res.batchCursor, res.batchRows, res.batchChunks = cursor, rows, chunks
			res.finished = e.now()
			return res
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO _dbmp.batch_chunks (run_id, chunk_no, execution_id, from_id, to_id, rows, down_sql)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			runID, chunks+1, spec.executionID, minID, maxID, n, m.SQLDown); err != nil {
			_ = tx.Rollback(context.Background())
			return fail(err, "checkpoint")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO _dbmp.batch_runs (run_id, execution_id, migration_id, status, cursor_id, committed_rows, committed_chunks)
			 VALUES ($1,$2,$3,'running',$4,$5,1)
			 ON CONFLICT (run_id) DO UPDATE SET execution_id = EXCLUDED.execution_id, status = 'running',
			   cursor_id = EXCLUDED.cursor_id,
			   committed_rows = _dbmp.batch_runs.committed_rows + EXCLUDED.committed_rows,
			   committed_chunks = _dbmp.batch_runs.committed_chunks + 1, updated_at = now()`,
			runID, spec.executionID, m.ID, maxID, n); err != nil {
			_ = tx.Rollback(context.Background())
			return fail(err, "checkpoint")
		}
		if err := tx.Commit(ctx); err != nil {
			return fail(err, "commit")
		}
		cursor, rows, chunks = maxID, rows+n, chunks+1
		observability.ObserveItem("success", e.now().Sub(chunkStarted).Milliseconds())
		_ = e.db.WithContext(ctx).Model(&models.Migration{}).Where("id = ?", m.ID).Updates(map[string]any{
			"batch_cursor": cursor, "batch_rows": rows, "batch_chunks": chunks, "batch_status": "running",
		}).Error
		if n < int64(m.BatchSize) {
			_ = markRun(ctx, pool, runID, "success")
			observability.ObserveBatch("success", e.now().Sub(res.started))
			res.status = models.ExecutionSuccess
			res.batchStatus = "success"
			res.batchCursor, res.batchRows, res.batchChunks = cursor, rows, chunks
			res.finished = e.now()
			return res
		}
	}
}

func (e *Engine) revertBatch(ctx context.Context, spec attemptSpec, m *models.Migration, pool *pgxpool.Pool, runID string) attemptResult {
	res := attemptResult{sql: m.SQL, started: e.now()}
	fail := func(err error, phase string) attemptResult {
		res.finished = e.now()
		res.status = models.ExecutionFailed
		res.errMsg = err.Error()
		res.batchStatus = "partially_reverted"
		_ = markRun(ctx, pool, runID, "partially_reverted")
		_ = e.db.WithContext(ctx).Model(&models.Migration{}).Where("id = ?", m.ID).
			Updates(map[string]any{"batch_status": "partially_reverted", "status": models.MigrationPartial}).Error
		e.log.WarnContext(ctx, "batch revert failed", "event", "batch.revert_failed", "phase", phase,
			"migration_id", m.ID, "execution_id", spec.executionID, "error", err.Error())
		return res
	}
	rows, err := pool.Query(ctx,
		`SELECT chunk_no, from_id, to_id, rows, down_sql FROM _dbmp.batch_chunks
		 WHERE run_id = $1 ORDER BY chunk_no DESC`, runID)
	if err != nil {
		return fail(err, "query")
	}
	defer rows.Close()
	type chunk struct {
		no       int64
		from, to int64
		count    int64
		down     string
	}
	var list []chunk
	for rows.Next() {
		var c chunk
		if err := rows.Scan(&c.no, &c.from, &c.to, &c.count, &c.down); err != nil {
			return fail(err, "scan")
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		return fail(err, "scan")
	}
	for _, c := range list {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fail(err, "begin")
		}
		if err := setStatementTimeout(ctx, tx, e.opts.StatementTimeout); err != nil {
			_ = tx.Rollback(context.Background())
			return fail(err, "timeout")
		}
		if _, err := tx.Exec(ctx, downSQL(c.down, c.from, c.to)); err != nil {
			_ = tx.Rollback(context.Background())
			return fail(err, "down")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM _dbmp.batch_chunks WHERE run_id = $1 AND chunk_no = $2`, runID, c.no); err != nil {
			_ = tx.Rollback(context.Background())
			return fail(err, "checkpoint")
		}
		if err := tx.Commit(ctx); err != nil {
			return fail(err, "commit")
		}
	}
	// Hitung sisa checkpoint dan cursor setelah revert.
	var remainingRows, remainingChunks int64
	_ = pool.QueryRow(ctx,
		`SELECT COALESCE(sum(rows),0), count(*) FROM _dbmp.batch_chunks WHERE run_id = $1`, runID).
		Scan(&remainingRows, &remainingChunks)
	var cursor int64
	_ = pool.QueryRow(ctx,
		`SELECT COALESCE(max(to_id),0) FROM _dbmp.batch_chunks WHERE run_id = $1`, runID).Scan(&cursor)
	if remainingChunks == 0 {
		_ = markRun(ctx, pool, runID, "reverted")
		res.batchStatus = "reverted"
	} else {
		_ = markRun(ctx, pool, runID, "partially_reverted")
		res.batchStatus = "partially_reverted"
	}
	res.status = models.ExecutionFailed
	res.errMsg = "batch cancelled"
	res.batchCursor, res.batchRows, res.batchChunks = cursor, remainingRows, remainingChunks
	res.finished = e.now()
	observability.ObserveBatch("cancelled", res.finished.Sub(res.started))
	return res
}

func (e *Engine) EnqueueResume(ctx context.Context, connectionID, migrationID, requestedBy string) (*models.MigrationJob, error) {
	var m models.Migration
	if err := e.db.WithContext(ctx).First(&m, "id = ? AND connection_id = ?", migrationID, connectionID).Error; err != nil {
		return nil, ErrMigrationNotFound
	}
	if m.BatchSize <= 0 || m.BatchRunID == nil {
		return nil, ErrNotExecutable
	}
	if m.Status != models.MigrationPartial && m.Status != models.MigrationApproved {
		return nil, ErrNotExecutable
	}
	if err := e.db.WithContext(ctx).Model(&models.Migration{}).Where("id = ?", m.ID).
		Updates(map[string]any{"status": models.MigrationApproved, "batch_cancel_requested": false}).Error; err != nil {
		return nil, err
	}
	return e.Enqueue(ctx, EnqueueRequest{
		ConnectionID: connectionID, MigrationID: m.ID, Kind: models.JobKindUp, RequestedBy: requestedBy,
	})
}
