package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"web_backend/internal/models"
	"web_backend/internal/telemetry"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// PoolProvider dipenuhi oleh *targetdb.Service.
type PoolProvider interface {
	Pool(ctx context.Context, connectionID string) (*pgxpool.Pool, error)
}

type Options struct {
	MaxAttempts         int           // attempt maksimum per job, termasuk yang pertama
	BackoffBase         time.Duration // jeda retry pertama, lalu berlipat dua
	BackoffMax          time.Duration
	PollEvery           time.Duration // interval worker memeriksa queue
	HeartbeatEvery      time.Duration
	StaleAfter          time.Duration // job running tanpa heartbeat selama ini dianggap terhenti
	StatementTimeout    time.Duration // batas eksekusi SQL migration di database target
	DenySuperuserTarget bool          // tolak eksekusi bila user target adalah superuser
	Logger              *slog.Logger
	// Revalidate dipanggil worker setelah lock didapat, sebelum SQL dijalankan, untuk aturan yang bergantung
	// pada riwayat (mis. rollback hanya untuk migration aktif terakhir). Boleh nil.
	Revalidate func(tx *gorm.DB, job *models.MigrationJob) error
}

func (o Options) withDefaults() Options {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = 2 * time.Second
	}
	if o.BackoffMax <= 0 {
		o.BackoffMax = 30 * time.Second
	}
	if o.PollEvery <= 0 {
		o.PollEvery = time.Second
	}
	if o.HeartbeatEvery <= 0 {
		o.HeartbeatEvery = 5 * time.Second
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 30 * time.Second
	}
	if o.StatementTimeout <= 0 {
		o.StatementTimeout = 5 * time.Minute
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}

type Engine struct {
	db     *gorm.DB
	sqlDB  *sql.DB
	pools  PoolProvider
	locker *Locker
	opts   Options
	log    *slog.Logger
	now    func() time.Time
	wake   chan struct{}
}

func New(db *gorm.DB, pools PoolProvider, opts Options) (*Engine, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	opts = opts.withDefaults()
	return &Engine{
		db:     db,
		sqlDB:  sqlDB,
		pools:  pools,
		locker: NewLocker(sqlDB),
		opts:   opts,
		log:    opts.Logger.With("component", "migration_engine"),
		now:    func() time.Time { return time.Now().UTC() },
		wake:   make(chan struct{}, 1),
	}, nil
}

// Locker dipakai komponen lain (mis. batch) agar memakai lock yang sama.
func (e *Engine) Locker() *Locker { return e.locker }

type EnqueueRequest struct {
	ConnectionID string
	MigrationID  string
	Kind         models.JobKind
	RollbackOf   string // execution ID yang dibatalkan (khusus rollback)
	RequestedBy  string
}

// Enqueue memvalidasi permintaan lalu memasukkannya ke queue tanpa menunggu SQL dijalankan
func (e *Engine) Enqueue(ctx context.Context, req EnqueueRequest) (job *models.MigrationJob, err error) {
	ctx, span := tracer.Start(ctx, "queue.enqueue", trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.String("migration.id", req.MigrationID),
			attribute.String("migration.kind", string(req.Kind)), attribute.String("db.connection_id", req.ConnectionID)))
	defer func() { endSpan(span, err) }()

	if _, err := uuid.Parse(req.MigrationID); err != nil {
		return nil, ErrMigrationNotFound
	}
	var m models.Migration
	if err := e.db.WithContext(ctx).
		First(&m, "id = ? AND connection_id = ?", req.MigrationID, req.ConnectionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMigrationNotFound
		}
		return nil, err
	}

	var rollbackOf *string
	switch req.Kind {
	case models.JobKindUp:
		if m.Status != models.MigrationApproved {
			return nil, ErrNotExecutable
		}
		if err := ValidateDefinition(&m); err != nil {
			return nil, err
		}
	case models.JobKindRollback:
		if m.BatchSize > 0 {
			return nil, ErrNotExecutable
		}
		if m.Irreversible {
			return nil, ErrIrreversible
		}
		if err := e.checkRollbackTarget(ctx, m.ID, req.RollbackOf); err != nil {
			return nil, err
		}
		rollbackOf = &req.RollbackOf
	case models.JobKindForward:
		if m.BatchSize > 0 {
			return nil, ErrNotExecutable
		}
		if m.Status != models.MigrationSuccess && m.Status != models.MigrationFailed {
			return nil, ErrNotExecutable
		}
	case models.JobKindRevert:
		if m.BatchSize <= 0 || m.BatchRunID == nil ||
			(m.Status != models.MigrationSuccess && m.Status != models.MigrationPartial) {
			return nil, ErrNotExecutable
		}
	default:
		return nil, ErrInvalidKind
	}

	job = &models.MigrationJob{
		ConnectionID: req.ConnectionID,
		MigrationID:  m.ID,
		ExecutionID:  uuid.NewString(),
		Kind:         req.Kind,
		Status:       models.JobQueued,
		RollbackOf:   rollbackOf,
		MaxAttempts:  e.opts.MaxAttempts,
		RunAfter:     e.now(),
		RequestedBy:  req.RequestedBy,
		TraceParent:  telemetry.Inject(ctx),
	}
	if err := e.db.WithContext(ctx).Create(job).Error; err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrJobActive
		}
		return nil, err
	}
	job.MigrationName = m.Name
	span.SetAttributes(attribute.String("migration.job_id", job.ID), attribute.String("migration.execution_id", job.ExecutionID))
	e.log.InfoContext(ctx, "job enqueued",
		"event", "job.enqueued", "actor", req.RequestedBy, "database", req.ConnectionID,
		"status", job.Status, "job_id", job.ID, "execution_id", job.ExecutionID,
		"migration_id", job.MigrationID, "kind", job.Kind)
	e.Wake()
	return job, nil
}

// revalidate memeriksa ulang, setelah lock didapat, bahwa job masih sah dijalankan terhadap keadaan terkini.
func (e *Engine) revalidate(ctx context.Context, spec attemptSpec, m *models.Migration) error {
	switch spec.kind {
	case models.JobKindUp:
		if m.Status != models.MigrationApproved && !(m.BatchSize > 0 && m.Status == models.MigrationPartial) {
			return fmt.Errorf("%w: migration status is %s", ErrNotExecutable, m.Status)
		}
		if err := ValidateDefinition(m); err != nil {
			return err
		}
	case models.JobKindRollback:
		if m.Irreversible {
			return ErrIrreversible
		}
		rollbackOf := ""
		if spec.rollbackOf != nil {
			rollbackOf = *spec.rollbackOf
		}
		if err := e.checkRollbackTarget(ctx, m.ID, rollbackOf); err != nil {
			return err
		}
	case models.JobKindForward:
		if m.Status != models.MigrationSuccess && m.Status != models.MigrationFailed {
			return fmt.Errorf("%w: migration status is %s", ErrNotExecutable, m.Status)
		}
	case models.JobKindRevert:
		if m.BatchSize <= 0 || m.BatchRunID == nil ||
			(m.Status != models.MigrationSuccess && m.Status != models.MigrationPartial) {
			return fmt.Errorf("%w: migration status is %s", ErrNotExecutable, m.Status)
		}
	}
	if e.opts.Revalidate == nil {
		return nil
	}
	return e.opts.Revalidate(e.db.WithContext(ctx), &models.MigrationJob{
		ConnectionID: spec.connectionID, MigrationID: spec.migrationID, Kind: spec.kind, RollbackOf: spec.rollbackOf,
	})
}

func (e *Engine) checkRollbackTarget(ctx context.Context, migrationID, executionID string) error {
	if _, err := uuid.Parse(executionID); err != nil {
		return ErrRollbackTarget
	}
	var count int64
	if err := e.db.WithContext(ctx).Model(&models.MigrationExecution{}).
		Where("execution_id = ? AND migration_id = ? AND status = ? AND kind IN ?",
			executionID, migrationID, models.ExecutionSuccess, []models.JobKind{models.JobKindUp, models.JobKindForward}).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrRollbackTarget
	}
	return nil
}

// Wake membangunkan satu worker di proses ini; worker di proses lain tetap menemukan job lewat polling.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) GetJob(ctx context.Context, connectionID, jobID string) (*models.MigrationJob, error) {
	if _, err := uuid.Parse(jobID); err != nil {
		return nil, ErrJobNotFound
	}
	var job models.MigrationJob
	if err := e.db.WithContext(ctx).First(&job, "id = ? AND connection_id = ?", jobID, connectionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrJobNotFound
		}
		return nil, err
	}
	if err := e.attachMigrationNames(ctx, []*models.MigrationJob{&job}); err != nil {
		return nil, err
	}
	return &job, nil
}

func (e *Engine) ListJobs(ctx context.Context, connectionID string, limit int) ([]models.MigrationJob, error) {
	jobs := make([]models.MigrationJob, 0)
	if err := e.db.WithContext(ctx).Where("connection_id = ?", connectionID).
		Order("created_at DESC").Limit(limit).Find(&jobs).Error; err != nil {
		return nil, err
	}
	ptrs := make([]*models.MigrationJob, len(jobs))
	for i := range jobs {
		ptrs[i] = &jobs[i]
	}
	return jobs, e.attachMigrationNames(ctx, ptrs)
}

// Executions mengembalikan seluruh attempt sebuah migration, urut kronologis.
func (e *Engine) Executions(ctx context.Context, migrationID string) ([]models.MigrationExecution, error) {
	executions := make([]models.MigrationExecution, 0)
	err := e.db.WithContext(ctx).Where("migration_id = ?", migrationID).Order("id").Find(&executions).Error
	return executions, err
}

func (e *Engine) attachMigrationNames(ctx context.Context, jobs []*models.MigrationJob) error {
	if len(jobs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.MigrationID)
	}
	var rows []struct {
		ID   string
		Name string
	}
	if err := e.db.WithContext(ctx).Model(&models.Migration{}).Select("id, name").
		Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return err
	}
	names := make(map[string]string, len(rows))
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	for _, j := range jobs {
		j.MigrationName = names[j.MigrationID]
	}
	return nil
}
