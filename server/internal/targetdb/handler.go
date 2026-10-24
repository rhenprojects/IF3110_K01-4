package targetdb

import (
	"errors"
	"log"
	"web_backend/internal/authctx"
	"web_backend/internal/httpx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(router fiber.Router, middleware ...fiber.Handler) {
	handlers := make([]fiber.Handler, 0, len(middleware)+2)
	handlers = append(handlers, middleware...)
	handlers = append(handlers, authctx.RequireAdmin, h.requireService)
	g := router.Group("/admin/connections", handlers...)

	g.Get("/", h.List)
	g.Post("/", h.Create)
	g.Post("/test", h.TestUnsaved)
	g.Get("/:id", h.Get)
	g.Put("/:id", h.Update)
	g.Delete("/:id", h.Delete)
	g.Post("/:id/test", h.TestSaved)
}

// RegisterUserRoutes menyediakan akses koneksi berdasarkan keanggotaan group
// dan level CanView/CanEdit, bukan hanya Platform Admin.
func (h *Handler) RegisterUserRoutes(router fiber.Router, middleware ...fiber.Handler) {
	handlers := make([]fiber.Handler, 0, len(middleware)+2)
	handlers = append(handlers, middleware...)
	handlers = append(handlers, authctx.RequireAuth, h.requireService)
	g := router.Group("/connections", handlers...)

	g.Get("/", h.ListAccessible)
	g.Get("/:id", h.GetAccessible)
	g.Put("/:id", h.UpdateAccessible)
	g.Post("/:id/test", h.TestSavedAccessible)
}

func (h *Handler) requireService(c *fiber.Ctx) error {
	if h.svc == nil {
		return httpx.Error(c, fiber.StatusServiceUnavailable, "Database is not connected")
	}
	return c.Next()
}

func (h *Handler) List(c *fiber.Ctx) error {
	conns, err := h.svc.List(c.UserContext())
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(models.ConnectionListResponse{Connections: conns})
}

func (h *Handler) Get(c *fiber.Ctx) error {
	conn, err := h.svc.Get(c.UserContext(), c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(conn)
}

func (h *Handler) Create(c *fiber.Ctx) error {
	var in ConnectionInput
	if err := c.BodyParser(&in); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}
	conn, err := h.svc.Create(c.UserContext(), in, authctx.UserID(c))
	if err != nil {
		return writeError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(conn)
}

func (h *Handler) Update(c *fiber.Ctx) error {
	var in ConnectionInput
	if err := c.BodyParser(&in); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}
	conn, err := h.svc.Update(c.UserContext(), c.Params("id"), in)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(conn)
}

func (h *Handler) Delete(c *fiber.Ctx) error {
	if err := h.svc.Delete(c.UserContext(), c.Params("id")); err != nil {
		return writeError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) TestUnsaved(c *fiber.Ctx) error {
	var in ConnectionInput
	if err := c.BodyParser(&in); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}
	version, err := h.svc.Test(c.UserContext(), in)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(models.ConnectionTestResponse{Status: "ok", ServerVersion: version})
}

func (h *Handler) TestSaved(c *fiber.Ctx) error {
	version, err := h.svc.TestSaved(c.UserContext(), c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(models.ConnectionTestResponse{Status: "ok", ServerVersion: version})
}

func writeError(c *fiber.Ctx, err error) error {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		return httpx.ValidationError(c, verr.Field, verr.Message)
	case errors.Is(err, ErrNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrNotFound.Error())
	case errors.Is(err, ErrDuplicateName):
		return httpx.Error(c, fiber.StatusConflict, ErrDuplicateName.Error())
	case errors.Is(err, ErrDecrypt):
		return httpx.Error(c, fiber.StatusInternalServerError, ErrDecrypt.Error()+"; please re-enter the password")
	}

	for _, connErr := range []error{ErrAuthFailed, ErrDatabaseNotFound, ErrTimeout, ErrUnreachable} {
		if errors.Is(err, connErr) {
			log.Printf("[targetdb] connection failed: %v", err)
			status := fiber.StatusBadGateway
			if connErr == ErrTimeout {
				status = fiber.StatusGatewayTimeout
			}
			return httpx.Error(c, status, connErr.Error())
		}
	}

	log.Printf("[targetdb] internal error: %v", err)
	return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
}

func (h *Handler) ListAccessible(c *fiber.Ctx) error {
	conns, err := h.svc.ListAccessible(c.UserContext(), authctx.UserID(c))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(models.ConnectionListResponse{Connections: conns})
}

func (h *Handler) GetAccessible(c *fiber.Ctx) error {
	allowed, err := h.svc.CanView(c.UserContext(), authctx.UserID(c), c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	if !allowed {
		return writeError(c, ErrNotFound)
	}
	return h.Get(c)
}

func (h *Handler) UpdateAccessible(c *fiber.Ctx) error {
	allowed, err := h.svc.CanEdit(c.UserContext(), authctx.UserID(c), c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	if !allowed {
		return writeError(c, ErrNotFound)
	}
	return h.Update(c)
}

func (h *Handler) TestSavedAccessible(c *fiber.Ctx) error {
	allowed, err := h.svc.CanView(c.UserContext(), authctx.UserID(c), c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	if !allowed {
		return writeError(c, ErrNotFound)
	}
	return h.TestSaved(c)
}
