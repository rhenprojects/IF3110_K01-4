package targetdb

import (
	"errors"
	"log"
	"web_backend/internal/authctx"

	"github.com/gofiber/fiber/v2"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(router fiber.Router) {
	g := router.Group("/admin/connections", authctx.RequireAdmin, h.requireService)

	g.Get("/", h.List)
	g.Post("/", h.Create)
	g.Post("/test", h.TestUnsaved)
	g.Get("/:id", h.Get)
	g.Put("/:id", h.Update)
	g.Delete("/:id", h.Delete)
	g.Post("/:id/test", h.TestSaved)
}

func (h *Handler) requireService(c *fiber.Ctx) error {
	if h.svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Database is not connected"})
	}
	return c.Next()
}

func (h *Handler) List(c *fiber.Ctx) error {
	conns, err := h.svc.List(c.UserContext())
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(fiber.Map{"connections": conns})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	version, err := h.svc.Test(c.UserContext(), in)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "server_version": version})
}

func (h *Handler) TestSaved(c *fiber.Ctx) error {
	version, err := h.svc.TestSaved(c.UserContext(), c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "server_version": version})
}

func writeError(c *fiber.Ctx, err error) error {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": verr.Message, "field": verr.Field})
	case errors.Is(err, ErrNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": ErrNotFound.Error()})
	case errors.Is(err, ErrDuplicateName):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": ErrDuplicateName.Error()})
	case errors.Is(err, ErrDecrypt):
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": ErrDecrypt.Error() + "; please re-enter the password"})
	}

	for _, connErr := range []error{ErrAuthFailed, ErrDatabaseNotFound, ErrTimeout, ErrUnreachable} {
		if errors.Is(err, connErr) {
			log.Printf("[targetdb] connection failed: %v", err)
			status := fiber.StatusBadGateway
			if connErr == ErrTimeout {
				status = fiber.StatusGatewayTimeout
			}
			return c.Status(status).JSON(fiber.Map{"error": connErr.Error()})
		}
	}

	log.Printf("[targetdb] internal error: %v", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Internal server error"})
}
