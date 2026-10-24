package databasehandler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
	"web_backend/internal/authctx"
	"web_backend/internal/authz"
	"web_backend/internal/httpx"
	"web_backend/internal/models"
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
)

type Handler struct {
	db    *gorm.DB
	svc   *targetdb.Service
	locks sync.Map
}

func NewHandler(db *gorm.DB, svc *targetdb.Service) *Handler {
	return &Handler{db: db, svc: svc}
}

func (h *Handler) requireDB(c *fiber.Ctx) error {
	if h.db == nil || h.svc == nil {
		return httpx.Error(c, fiber.StatusServiceUnavailable, "Database is not connected")
	}
	return c.Next()
}

func (h *Handler) RegisterRoutes(router fiber.Router, middleware ...fiber.Handler) {
	handlers := make([]fiber.Handler, 0, len(middleware)+2)
	handlers = append(handlers, middleware...)
	handlers = append(handlers, authctx.RequireAuth, h.requireDB)
	g := router.Group("/databases", handlers...)

	g.Get("/", h.List)
	g.Get("/:id", h.Get)
	g.Get("/:id/schema", h.Schema)
	g.Get("/:id/tables/:schema/:table/rows", h.Rows)
	g.Get("/:id/migrations", h.ListMigrations)
	g.Get("/:id/migrations/:mid", h.GetMigration)
	g.Post("/:id/migrations", h.CreateMigration)
	g.Put("/:id/migrations/:mid", h.UpdateMigration)
	g.Post("/:id/migrations/:mid/execute", h.ExecuteMigration)
}

func (h *Handler) List(c *fiber.Ctx) error {
	conns, err := h.svc.List(c.UserContext())
	if err != nil {
		return h.writeError(c, err)
	}
	userID := authctx.UserID(c)
	items := make([]models.DatabaseItem, 0, len(conns))
	for _, conn := range conns {
		level, err := authz.Level(c.UserContext(), h.db, userID, conn.ID)
		if err != nil {
			return h.writeError(c, err)
		}
		if level == models.AccessNone {
			continue
		}
		items = append(items, models.DatabaseItem{
			ID: conn.ID, Name: conn.Name, DBName: conn.DBName, Level: level,
		})
	}
	return c.JSON(models.DatabaseListResponse{Databases: items})
}

func (h *Handler) Get(c *fiber.Ctx) error {
	conn, level, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(models.DatabaseItem{
		ID: conn.ID, Name: conn.Name, DBName: conn.DBName, Level: level,
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
	schemas, err := readSchema(c.UserContext(), pool)
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
	pool, err := h.pool(c, conn.ID)
	if err != nil {
		return h.writeError(c, err)
	}
	result, err := readRows(c.UserContext(), pool, c.Params("schema"), c.Params("table"), limit, offset)
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
	name, script, err := parseMigrationInput(c)
	if err != nil {
		return h.writeError(c, err)
	}
	migration := models.Migration{
		ConnectionID: conn.ID,
		Name:         name,
		SQL:          script,
		Status:       models.MigrationDraft,
		Error:        "",
		CreatedBy:    authctx.UserID(c),
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
	name, script, err := parseMigrationInput(c)
	if err != nil {
		return h.writeError(c, err)
	}
	migration.Name = name
	migration.SQL = script
	if err := h.db.WithContext(c.UserContext()).Save(migration).Error; err != nil {
		return h.writeError(c, err)
	}
	if err := h.attachUserNames(c.UserContext(), []*models.Migration{migration}); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(migration)
}

func (h *Handler) ExecuteMigration(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, true)
	if err != nil {
		return h.writeError(c, err)
	}

	lock := h.lockFor(conn.ID)
	if !lock.TryLock() {
		return h.writeError(c, ErrMigrationRunning)
	}
	defer lock.Unlock()

	migration, err := h.findMigration(c, conn.ID, c.Params("mid"))
	if err != nil {
		return h.writeError(c, err)
	}
	if migration.Status != models.MigrationDraft {
		return h.writeError(c, ErrMigrationNotDraft)
	}

	pool, err := h.pool(c, conn.ID)
	if err != nil {
		return h.writeError(c, err)
	}

	started := time.Now()
	tx, err := pool.Begin(c.UserContext())
	if err != nil {
		return h.writeError(c, err)
	}
	_, execErr := tx.Exec(c.UserContext(), migration.SQL)
	duration := time.Since(started).Milliseconds()
	if duration < 1 {
		duration = 1
	}

	now := time.Now().UTC()
	userID := authctx.UserID(c)
	migration.ExecutedBy = &userID
	migration.ExecutedAt = &now
	migration.DurationMs = &duration

	if execErr != nil {
		_ = tx.Rollback(c.UserContext())
		migration.Status = models.MigrationFailed
		migration.Error = execErr.Error()
		migration.Version = nil
		if err := h.db.WithContext(c.UserContext()).Save(migration).Error; err != nil {
			return h.writeError(c, err)
		}
		if err := h.attachUserNames(c.UserContext(), []*models.Migration{migration}); err != nil {
			return h.writeError(c, err)
		}
		return c.JSON(migration)
	}

	if err := tx.Commit(c.UserContext()); err != nil {
		_ = tx.Rollback(c.UserContext())
		return h.writeError(c, err)
	}

	version, err := h.nextVersion(c.UserContext(), conn.ID)
	if err != nil {
		return h.writeError(c, err)
	}
	migration.Status = models.MigrationSuccess
	migration.Error = ""
	migration.Version = &version
	if err := h.db.WithContext(c.UserContext()).Save(migration).Error; err != nil {
		return h.writeError(c, err)
	}
	if err := h.attachUserNames(c.UserContext(), []*models.Migration{migration}); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(migration)
}

func (h *Handler) nextVersion(ctx context.Context, connectionID string) (int, error) {
	var max sql.NullInt64
	if err := h.db.WithContext(ctx).
		Raw("SELECT MAX(version) FROM migrations WHERE connection_id = ? AND status = ?", connectionID, models.MigrationSuccess).
		Scan(&max).Error; err != nil {
		return 0, err
	}
	if !max.Valid {
		return 1, nil
	}
	return int(max.Int64) + 1, nil
}

func parseMigrationInput(c *fiber.Ctx) (string, string, error) {
	var req struct {
		Name string `json:"name"`
		SQL  string `json:"sql"`
	}
	if err := c.BodyParser(&req); err != nil {
		return "", "", &validationError{"name", "invalid request body"}
	}
	name := strings.TrimSpace(req.Name)
	switch {
	case name == "" || len(name) > 200:
		return "", "", &validationError{"name", "required, max 200 characters"}
	case strings.TrimSpace(req.SQL) == "":
		return "", "", &validationError{"sql", "required"}
	}
	return name, req.SQL, nil
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

func (h *Handler) lockFor(connectionID string) *sync.Mutex {
	value, _ := h.locks.LoadOrStore(connectionID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (h *Handler) writeError(c *fiber.Ctx, err error) error {
	var verr *validationError
	switch {
	case errors.As(err, &verr):
		return httpx.ValidationError(c, verr.Field, verr.Message)
	case errors.Is(err, ErrDatabaseNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrDatabaseNotFound.Error())
	case errors.Is(err, ErrNoDatabaseAccess), errors.Is(err, ErrEditAccessRequired):
		return httpx.Error(c, fiber.StatusForbidden, err.Error())
	case errors.Is(err, ErrTableNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrTableNotFound.Error())
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
