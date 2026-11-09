package databasehandler

import (
	"errors"
	"strconv"

	"web_backend/internal/authctx"
	"web_backend/internal/batch"
	"web_backend/internal/httpx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func (h *Handler) RegisterBatchRoutes(router fiber.Router, middleware ...fiber.Handler) {
	handlers := make([]fiber.Handler, 0, len(middleware)+2)
	handlers = append(handlers, middleware...)
	handlers = append(handlers, authctx.RequireAuth, h.requireDB)
	g := router.Group("/databases", handlers...)

	g.Post("/:id/batches", h.CreateBatch)
	g.Get("/:id/batches", h.ListBatches)
	g.Get("/:id/batches/:bid", h.GetBatch)
	g.Get("/:id/batches/:bid/items", h.ListBatchItems)
	g.Post("/:id/batches/:bid/cancel", h.CancelBatch)
	g.Post("/:id/batches/:bid/retry", h.RetryBatch)
	g.Get("/:id/executions", h.ListExecutions)
}

func (h *Handler) CreateBatch(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, true)
	if err != nil {
		return h.writeError(c, err)
	}
	var req struct {
		MigrationIDs []string `json:"migration_ids"`
	}
	if err := c.BodyParser(&req); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "invalid request body")
	}
	coordinator := h.NewBatchCoordinator()
	b, err := coordinator.Create(c.UserContext(), conn.ID, authctx.UserID(c), req.MigrationIDs)
	if err != nil {
		return h.writeBatchError(c, err)
	}
	if err := coordinator.Start(b); err != nil {
		return h.writeBatchError(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(b)
}

func (h *Handler) ListBatches(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	batches, err := h.NewBatchCoordinator().Store().List(c.UserContext(), conn.ID, queryInt(c.Query("limit"), 20, 1, 100))
	if err != nil {
		return h.writeError(c, err)
	}
	ptrs := make([]*models.MigrationBatch, len(batches))
	for i := range batches {
		ptrs[i] = &batches[i]
	}
	if err := h.attachBatchNames(c, ptrs); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(fiber.Map{"batches": batches})
}

func (h *Handler) GetBatch(c *fiber.Ctx) error {
	b, err := h.batchForRequest(c)
	if err != nil {
		return h.writeError(c, err)
	}
	if err := h.attachBatchNames(c, []*models.MigrationBatch{b}); err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(b)
}

func (h *Handler) ListBatchItems(c *fiber.Ctx) error {
	b, err := h.batchForRequest(c)
	if err != nil {
		return h.writeError(c, err)
	}
	limit := queryInt(c.Query("limit"), 100, 1, 200)
	afterID := queryInt64(c.Query("after_id"))
	items, err := h.NewBatchCoordinator().Store().Items(c.UserContext(), b.ID, afterID, limit)
	if err != nil {
		return h.writeError(c, err)
	}
	next := int64(0)
	if len(items) > 0 {
		next = items[len(items)-1].ID
	}
	return c.JSON(fiber.Map{"items": items, "next_after_id": next})
}

func (h *Handler) CancelBatch(c *fiber.Ctx) error {
	b, err := h.batchForRequest(c)
	if err != nil {
		return h.writeError(c, err)
	}
	if err := h.NewBatchCoordinator().Cancel(c.UserContext(), b.ID, authctx.UserID(c)); err != nil {
		return h.writeBatchError(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "cancel_requested"})
}

func (h *Handler) RetryBatch(c *fiber.Ctx) error {
	b, err := h.batchForRequest(c)
	if err != nil {
		return h.writeError(c, err)
	}
	coordinator := h.NewBatchCoordinator()
	retried, err := coordinator.Retry(c.UserContext(), b, authctx.UserID(c))
	if err != nil {
		return h.writeBatchError(c, err)
	}
	if err := coordinator.Start(retried); err != nil {
		return h.writeBatchError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(retried)
}

func (h *Handler) ListExecutions(c *fiber.Ctx) error {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return h.writeError(c, err)
	}
	limit := queryInt(c.Query("limit"), 50, 1, 200)
	afterID := queryInt64(c.Query("after_id"))
	items, err := h.NewBatchCoordinator().Store().Executions(
		c.UserContext(), conn.ID, c.Query("batch_id"), c.Query("migration_id"), afterID, limit)
	if err != nil {
		return h.writeError(c, err)
	}
	next := int64(0)
	if len(items) > 0 {
		next = items[len(items)-1].ID
	}
	return c.JSON(fiber.Map{"items": items, "next_after_id": next})
}

// batchForRequest resolves the batch and rejects IDs that belong to another
// connection so cross-tenant probes return 404 instead of leaking existence.
func (h *Handler) batchForRequest(c *fiber.Ctx) (*models.MigrationBatch, error) {
	conn, _, err := h.accessible(c, false)
	if err != nil {
		return nil, err
	}
	id := c.Params("bid")
	if _, err := uuid.Parse(id); err != nil {
		return nil, batch.ErrBatchNotFound
	}
	b, err := h.NewBatchCoordinator().Store().Get(c.UserContext(), id)
	if err != nil {
		return nil, err
	}
	if b.ConnectionID != conn.ID {
		return nil, batch.ErrBatchNotFound
	}
	return b, nil
}

func (h *Handler) attachBatchNames(c *fiber.Ctx, batches []*models.MigrationBatch) error {
	ids := make([]string, 0, len(batches)*2)
	for _, b := range batches {
		ids = append(ids, b.CreatedBy)
		if b.CancelledBy != nil {
			ids = append(ids, *b.CancelledBy)
		}
	}
	var users []models.User
	if err := h.db.WithContext(c.UserContext()).Select("id, username, name").Where("id::text IN ?", ids).Find(&users).Error; err != nil {
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
	for _, b := range batches {
		b.CreatedByName = names[b.CreatedBy]
		if b.CancelledBy != nil {
			name := names[*b.CancelledBy]
			b.CancelledByName = &name
		}
	}
	return nil
}

func (h *Handler) writeBatchError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, batch.ErrNoMigrations):
		return httpx.ValidationError(c, "migration_ids", batch.ErrNoMigrations.Error())
	case errors.Is(err, batch.ErrTooManyMigrations):
		return httpx.ValidationError(c, "migration_ids", batch.ErrTooManyMigrations.Error())
	case errors.Is(err, batch.ErrNotDraft):
		return httpx.Error(c, fiber.StatusConflict, batch.ErrNotDraft.Error())
	case errors.Is(err, batch.ErrBusy):
		return httpx.Error(c, fiber.StatusConflict, batch.ErrBusy.Error())
	case errors.Is(err, batch.ErrBatchNotFound):
		return httpx.Error(c, fiber.StatusNotFound, batch.ErrBatchNotFound.Error())
	case errors.Is(err, batch.ErrBatchTerminal):
		return httpx.Error(c, fiber.StatusConflict, batch.ErrBatchTerminal.Error())
	case errors.Is(err, batch.ErrNothingToRetry):
		return httpx.Error(c, fiber.StatusConflict, batch.ErrNothingToRetry.Error())
	}
	return h.writeError(c, err)
}

func queryInt64(raw string) int64 {
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
