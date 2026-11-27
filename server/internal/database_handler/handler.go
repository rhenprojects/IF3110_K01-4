package databasehandler

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strconv"
	"strings"
	"web_backend/internal/authctx"
	"web_backend/internal/authz"
	"web_backend/internal/engine"
	"web_backend/internal/httpx"
	"web_backend/internal/models"
	"web_backend/internal/review"
	"web_backend/internal/targetdb"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

var (
	ErrDatabaseNotFound   = errors.New("database not found")
	ErrMigrationNotFound  = errors.New("migration not found")
	ErrMigrationNotDraft  = errors.New("only draft migrations can be changed")
	ErrMigrationRunning   = errors.New("another migration is already running on this database")
	ErrNoDatabaseAccess   = errors.New("no access to this database")
	ErrEditAccessRequired = errors.New("edit access required")
	ErrMigrationImmutable = errors.New("migration has been executed; its SQL up/down is immutable, create a new migration instead")
)

type Handler struct {
	db     *gorm.DB
	svc    *targetdb.Service
	engine *engine.Engine
	review *review.Service
}

func NewHandler(db *gorm.DB, svc *targetdb.Service) *Handler {
	return NewHandlerWithEngine(db, svc, nil)
}

// NewHandlerWithEngine memakai engine bersama yang worker-nya sudah berjalan
func NewHandlerWithEngine(db *gorm.DB, svc *targetdb.Service, eng *engine.Engine) *Handler {
	if eng == nil && db != nil && svc != nil {
		if created, err := engine.New(db, svc, engine.Options{}); err == nil {
			eng = created
		}
	}
	h := &Handler{db: db, svc: svc, engine: eng}
	if db != nil && eng != nil {
		h.review = review.New(db, review.Deps{Enqueue: reviewEnqueue(eng), Validate: engine.ValidateDefinition})
	}
	return h
}

// Engine mengembalikan execution engine yang dipakai handler ini.
func (h *Handler) Engine() *engine.Engine { return h.engine }

func (h *Handler) requireDB(c *fiber.Ctx) error {
	if h.db == nil || h.svc == nil || h.engine == nil {
		return httpx.Error(c, fiber.StatusServiceUnavailable, "Database is not connected")
	}
	return c.Next()
}

// RegisterRoutes mendaftarkan route database; router protected sudah terautentikasi.
func (h *Handler) RegisterRoutes(router fiber.Router) {
	// Inbox leader lintas database; didaftarkan di luar grup /databases karena tidak terikat satu database.
	router.Get("/change-requests/pending", h.requireDB, h.ListPendingChangeRequests)

	g := router.Group("/databases", h.requireDB)

	g.Get("/", h.List)
	g.Get("/:id", h.Get)
	g.Get("/:id/schema", h.Schema)
	g.Get("/:id/tables/:schema/:table/rows", h.Rows)
	g.Get("/:id/migrations", h.ListMigrations)
	g.Get("/:id/migrations/:mid", h.GetMigration)
	g.Post("/:id/migrations", h.CreateMigration)
	g.Put("/:id/migrations/:mid", h.UpdateMigration)
	g.Get("/:id/migrations/:mid/executions", h.ListMigrationExecutions)
	g.Post("/:id/migrations/:mid/change-requests", h.SubmitChangeRequest)
	g.Post("/:id/migrations/:mid/batch/cancel", h.CancelBatchMigration)
	g.Post("/:id/migrations/:mid/batch/resume", h.ResumeBatchMigration)
	g.Post("/:id/migrations/:mid/batch/revert", h.RevertBatchMigration)
	g.Get("/:id/change-requests", h.ListChangeRequests)
	g.Post("/:id/change-requests/:rid/approve", h.ApproveChangeRequest)
	g.Post("/:id/change-requests/:rid/reject", h.RejectChangeRequest)
	g.Post("/:id/change-requests/:rid/dispatch", h.DispatchChangeRequest)
	g.Get("/:id/history", h.ListHistory)
	g.Get("/:id/jobs", h.ListJobs)
	g.Get("/:id/jobs/:jid", h.GetJob)
}

func (h *Handler) List(c *fiber.Ctx) error {
	conns, err := h.svc.List(c.UserContext())
	if err != nil {
		return h.writeError(c, err)
	}
	userID := authctx.UserID(c)
	ids := make([]string, len(conns))
	for i, conn := range conns {
		ids[i] = conn.ID
	}
	access, err := authz.AccessFor(c.UserContext(), h.db, userID, ids)
	if err != nil {
		return h.writeError(c, err)
	}
	items := make([]models.DatabaseItem, 0, len(conns))
	for _, conn := range conns {
		a := access[conn.ID]
		if a.Level == models.AccessNone {
			continue
		}
		items = append(items, models.DatabaseItem{
			ID: conn.ID, Name: conn.Name, DBName: conn.DBName, Level: a.Level, CanBatch: a.CanBatch,
		})
	}
	return c.JSON(models.DatabaseListResponse{Databases: items})
}

func (h *Handler) Get(c *fiber.Ctx) error {
	conn, level, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	access, err := authz.AccessFor(c.UserContext(), h.db, authctx.UserID(c), []string{conn.ID})
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.DatabaseItem{
		ID: conn.ID, Name: conn.Name, DBName: conn.DBName, Level: level, CanBatch: access[conn.ID].CanBatch,
	})
}

func (h *Handler) Schema(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	pool, err := h.pool(c, conn.ID)
	if err != nil {
		return h.writeError(c, err)
	}
	var schemas []models.SchemaInfo
	err = readOnly(c.UserContext(), pool, func(q querier) (err error) {
		schemas, err = readSchema(c.UserContext(), q)
		return err
	})
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.SchemaResponse{Schemas: schemas})
}

func (h *Handler) Rows(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	limit := queryInt(c.Query("limit"), 50, 1, 200)
	offset := queryInt(c.Query("offset"), 0, 0, 1<<31-1)
	schema, errSchema := url.PathUnescape(c.Params("schema"))
	table, errTable := url.PathUnescape(c.Params("table"))
	if errSchema != nil || errTable != nil {
		return h.writeError(c, ErrTableNotFound)
	}
	pool, err := h.pool(c, conn.ID)
	if err != nil {
		return h.writeError(c, err)
	}
	var result *models.RowsResponse
	err = readOnly(c.UserContext(), pool, func(q querier) (err error) {
		result, err = readRows(c.UserContext(), q, schema, table, limit, offset)
		return err
	})
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(result)
}

func queryInt(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func (h *Handler) ListMigrations(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	var migrations []models.Migration
	if err := h.db.WithContext(c.UserContext()).
		Where("connection_id = ?", conn.ID).
		Order("created_at DESC").
		Find(&migrations).Error; err != nil {
		return h.writeError(c, err)
	}
	ptrs := make([]*models.Migration, len(migrations))
	for i := range migrations {
		ptrs[i] = &migrations[i]
	}
	if err := h.attachUserNames(c.UserContext(), ptrs); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.MigrationListResponse{Migrations: migrations})
}

func (h *Handler) GetMigration(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	migration, err := h.findMigration(c, conn.ID, c.Params("mid"))
	if err != nil {
		return h.writeError(c, err)
	}
	if err := h.attachUserNames(c.UserContext(), []*models.Migration{migration}); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(migration)
}

func (h *Handler) CreateMigration(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, true)
	if err != nil {
		return h.writeError(c, err)
	}
	input, err := parseMigrationInput(c)
	if err != nil {
		return h.writeError(c, err)
	}
	migration := models.Migration{
		ConnectionID:       conn.ID,
		Name:               input.Name,
		SQL:                input.SQL,
		SQLDown:            input.SQLDown,
		Irreversible:       input.Irreversible,
		IrreversibleReason: input.IrreversibleReason,
		BatchSize:          input.BatchSize,
		Revision:           1,
		Status:             models.MigrationDraft,
		Error:              "",
		CreatedBy:          authctx.UserID(c),
	}
	if err := h.db.WithContext(c.UserContext()).Create(&migration).Error; err != nil {
		return h.writeError(c, err)
	}
	if err := h.attachUserNames(c.UserContext(), []*models.Migration{&migration}); err != nil {
		return h.writeError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(migration)
}

func (h *Handler) UpdateMigration(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, true)
	if err != nil {
		return h.writeError(c, err)
	}
	migration, err := h.findMigration(c, conn.ID, c.Params("mid"))
	if err != nil {
		return h.writeError(c, err)
	}
	if migration.Status != models.MigrationDraft {
		return h.writeError(c, ErrMigrationNotDraft)
	}
	// SQL up/down yang pernah masuk queue (berhasil, gagal, atau masih menunggu) tidak boleh diubah; perbaikan dilakukan lewat migration baru.
	var jobs int64
	if err := h.db.WithContext(c.UserContext()).Model(&models.MigrationJob{}).
		Where("migration_id = ?", migration.ID).Count(&jobs).Error; err != nil {
		return h.writeError(c, err)
	}
	if jobs > 0 {
		return h.writeError(c, ErrMigrationImmutable)
	}
	input, err := parseMigrationInput(c)
	if err != nil {
		return h.writeError(c, err)
	}
	migration.Name = input.Name
	migration.SQL = input.SQL
	migration.SQLDown = input.SQLDown
	migration.Irreversible = input.Irreversible
	migration.IrreversibleReason = input.IrreversibleReason
	migration.BatchSize = input.BatchSize
	migration.Revision++
	if err := h.db.WithContext(c.UserContext()).Save(migration).Error; err != nil {
		return h.writeError(c, err)
	}
	if err := h.attachUserNames(c.UserContext(), []*models.Migration{migration}); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(migration)
}

func (h *Handler) CancelBatchMigration(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, true)
	if err != nil {
		return h.writeError(c, err)
	}
	m, err := h.findMigration(c, conn.ID, c.Params("mid"))
	if err != nil {
		return h.writeError(c, err)
	}
	if m.BatchSize <= 0 {
		return httpx.ValidationError(c, "batch_size", "migration is not a batch migration")
	}
	if err := h.db.WithContext(c.UserContext()).Model(&models.Migration{}).
		Where("id = ?", m.ID).Update("batch_cancel_requested", true).Error; err != nil {
		return h.writeError(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "cancel_requested"})
}

func (h *Handler) ResumeBatchMigration(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, true)
	if err != nil {
		return h.writeError(c, err)
	}
	m, err := h.findMigration(c, conn.ID, c.Params("mid"))
	if err != nil {
		return h.writeError(c, err)
	}
	if m.BatchSize <= 0 || m.BatchRunID == nil {
		return httpx.Error(c, fiber.StatusConflict, "migration has no batch checkpoint to resume")
	}
	job, err := h.engine.EnqueueResume(c.UserContext(), conn.ID, m.ID, authctx.UserID(c))
	if err != nil {
		return h.writeError(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(job)
}

func (h *Handler) RevertBatchMigration(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, true)
	if err != nil {
		return h.writeError(c, err)
	}
	m, err := h.findMigration(c, conn.ID, c.Params("mid"))
	if err != nil {
		return h.writeError(c, err)
	}
	if m.BatchSize <= 0 || m.BatchRunID == nil {
		return httpx.ValidationError(c, "batch_size", "migration is not a batch migration")
	}
	updated, err := h.engine.RevertBatch(c.UserContext(), conn.ID, m.ID, authctx.UserID(c))
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(updated)
}

func (h *Handler) ListJobs(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	jobs, err := h.engine.ListJobs(c.UserContext(), conn.ID, queryInt(c.Query("limit"), 50, 1, 200))
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.JobListResponse{Jobs: jobs})
}

func (h *Handler) GetJob(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	job, err := h.engine.GetJob(c.UserContext(), conn.ID, c.Params("jid"))
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(job)
}

// ListMigrationExecutions mengembalikan seluruh attempt migration beserta salinan SQL yang dijalankan
func (h *Handler) ListMigrationExecutions(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	migration, err := h.findMigration(c, conn.ID, c.Params("mid"))
	if err != nil {
		return h.writeError(c, err)
	}
	executions, err := h.engine.Executions(c.UserContext(), migration.ID)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.ExecutionListResponse{Executions: executions})
}

type migrationInput struct {
	Name               string `json:"name"`
	SQL                string `json:"sql"`
	SQLDown            string `json:"sql_down"`
	Irreversible       bool   `json:"irreversible"`
	IrreversibleReason string `json:"irreversible_reason"`
	BatchSize          int    `json:"batch_size"`
}

// parseMigrationInput hanya mewajibkan nama dan SQL up; draft boleh belum punya SQL down
func parseMigrationInput(c *fiber.Ctx) (migrationInput, error) {
	var req migrationInput
	if err := c.BodyParser(&req); err != nil {
		return req, &validationError{"name", "invalid request body"}
	}
	req.Name = strings.TrimSpace(req.Name)
	req.IrreversibleReason = strings.TrimSpace(req.IrreversibleReason)
	switch {
	case req.Name == "" || len(req.Name) > 200:
		return req, &validationError{"name", "required, max 200 characters"}
	case strings.TrimSpace(req.SQL) == "":
		return req, &validationError{"sql", "required"}
	case req.BatchSize < 0 || req.BatchSize > 1000000:
		return req, &validationError{"batch_size", "must be between 0 and 1000000"}
	}
	if !req.Irreversible {
		req.IrreversibleReason = ""
	}
	return req, nil
}

type validationError struct {
	Field   string
	Message string
}

func (e *validationError) Error() string { return e.Message }

func (h *Handler) accessible(c *fiber.Ctx, requireEdit bool) (*models.DBConnection, models.AccessLevel, error) {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return nil, models.AccessNone, ErrDatabaseNotFound
	}
	var conn models.DBConnection
	if err := h.db.WithContext(c.UserContext()).First(&conn, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, models.AccessNone, ErrDatabaseNotFound
		}
		return nil, models.AccessNone, err
	}
	level, err := authz.Level(c.UserContext(), h.db, authctx.UserID(c), conn.ID)
	if err != nil {
		return nil, models.AccessNone, err
	}
	if level == models.AccessNone {
		return nil, models.AccessNone, ErrNoDatabaseAccess
	}
	if requireEdit && level != models.AccessEdit {
		return nil, models.AccessNone, ErrEditAccessRequired
	}
	return &conn, level, nil
}

func (h *Handler) pool(c *fiber.Ctx, connectionID string) (*pgxpool.Pool, error) {
	return h.svc.Pool(c.UserContext(), connectionID)
}

func (h *Handler) findMigration(c *fiber.Ctx, connectionID, migrationID string) (*models.Migration, error) {
	if _, err := uuid.Parse(migrationID); err != nil {
		return nil, ErrMigrationNotFound
	}
	var migration models.Migration
	if err := h.db.WithContext(c.UserContext()).
		First(&migration, "id = ? AND connection_id = ?", migrationID, connectionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMigrationNotFound
		}
		return nil, err
	}
	return &migration, nil
}

func (h *Handler) attachUserNames(ctx context.Context, migrations []*models.Migration) error {
	ids := make([]string, 0, len(migrations)*2)
	seen := make(map[string]bool)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, migration := range migrations {
		add(migration.CreatedBy)
		if migration.ExecutedBy != nil {
			add(*migration.ExecutedBy)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	var users []models.User
	if err := h.db.WithContext(ctx).Select("id, username, name").Where("id::text IN ?", ids).Find(&users).Error; err != nil {
		return err
	}
	names := make(map[string]string, len(users))
	for _, user := range users {
		name := user.Name
		if name == "" {
			name = user.Username
		}
		names[user.ID] = name
	}
	for _, migration := range migrations {
		migration.CreatedByName = names[migration.CreatedBy]
		if migration.ExecutedBy != nil {
			name := names[*migration.ExecutedBy]
			migration.ExecutedByName = &name
		}
	}
	return nil
}

func (h *Handler) writeError(c *fiber.Ctx, err error) error {
	var verr *validationError
	var engineVerr *engine.ValidationError
	switch {
	case errors.As(err, &verr):
		return httpx.ValidationError(c, verr.Field, verr.Message)
	case errors.As(err, &engineVerr):
		return httpx.ValidationError(c, engineVerr.Field, engineVerr.Message)
	case errors.Is(err, engine.ErrMigrationNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrMigrationNotFound.Error())
	case errors.Is(err, engine.ErrJobNotFound):
		return httpx.Error(c, fiber.StatusNotFound, engine.ErrJobNotFound.Error())
	case errors.Is(err, engine.ErrJobActive), errors.Is(err, engine.ErrNotExecutable),
		errors.Is(err, engine.ErrIrreversible), errors.Is(err, ErrMigrationImmutable):
		return httpx.Error(c, fiber.StatusConflict, err.Error())
	case errors.Is(err, engine.ErrRollbackTarget), errors.Is(err, engine.ErrInvalidKind), errors.Is(err, review.ErrInvalidKind):
		return httpx.Error(c, fiber.StatusBadRequest, err.Error())
	case errors.Is(err, review.ErrMigrationNotFound), errors.Is(err, review.ErrRequestNotFound):
		return httpx.Error(c, fiber.StatusNotFound, err.Error())
	case errors.Is(err, review.ErrNotLeader), errors.Is(err, review.ErrEditAccessRequired):
		return httpx.Error(c, fiber.StatusForbidden, err.Error())
	case isReviewConflict(err):
		return httpx.Error(c, fiber.StatusConflict, err.Error())
	case errors.Is(err, review.ErrBatchRevert):
		return httpx.Error(c, fiber.StatusConflict, err.Error())
	case errors.Is(err, review.ErrDispatchFailed):
		log.Printf("[database] %v", err)
		return httpx.Error(c, fiber.StatusBadGateway, review.ErrDispatchFailed.Error())
	case errors.Is(err, ErrDatabaseNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrDatabaseNotFound.Error())
	case errors.Is(err, ErrNoDatabaseAccess), errors.Is(err, ErrEditAccessRequired):
		return httpx.Error(c, fiber.StatusForbidden, err.Error())
	case errors.Is(err, ErrTableNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrTableNotFound.Error())
	case errors.Is(err, ErrTargetPermission):
		log.Printf("[database] %v", err)
		return httpx.Error(c, fiber.StatusForbidden, ErrTargetPermission.Error())
	case errors.Is(err, ErrQueryTimeout):
		log.Printf("[database] %v", err)
		return httpx.Error(c, fiber.StatusGatewayTimeout, ErrQueryTimeout.Error())
	case errors.Is(err, ErrQueryFailed):
		log.Printf("[database] %v", err)
		return httpx.Error(c, fiber.StatusBadGateway, ErrQueryFailed.Error())
	case errors.Is(err, ErrMigrationNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrMigrationNotFound.Error())
	case errors.Is(err, ErrMigrationNotDraft):
		return httpx.Error(c, fiber.StatusConflict, ErrMigrationNotDraft.Error())
	case errors.Is(err, ErrMigrationRunning):
		return httpx.Error(c, fiber.StatusConflict, ErrMigrationRunning.Error())
	case errors.Is(err, targetdb.ErrNotFound):
		return httpx.Error(c, fiber.StatusNotFound, targetdb.ErrNotFound.Error())
	case errors.Is(err, targetdb.ErrTimeout):
		log.Printf("[database] target timeout: %v", err)
		return httpx.Error(c, fiber.StatusGatewayTimeout, targetdb.ErrTimeout.Error())
	case errors.Is(err, targetdb.ErrAuthFailed):
		log.Printf("[database] target authentication failed: %v", err)
		return httpx.Error(c, fiber.StatusBadGateway, targetdb.ErrAuthFailed.Error())
	case errors.Is(err, targetdb.ErrDatabaseNotFound):
		log.Printf("[database] target database missing: %v", err)
		return httpx.Error(c, fiber.StatusBadGateway, targetdb.ErrDatabaseNotFound.Error())
	case errors.Is(err, targetdb.ErrUnreachable):
		log.Printf("[database] target unreachable: %v", err)
		return httpx.Error(c, fiber.StatusBadGateway, targetdb.ErrUnreachable.Error())
	case errors.Is(err, targetdb.ErrDecrypt):
		log.Printf("[database] stored credentials cannot be decrypted: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, targetdb.ErrDecrypt.Error()+"; please re-enter the password")
	}
	log.Printf("[database] internal error: %v", err)
	return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
}
