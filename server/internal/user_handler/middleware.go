package userhandler

import (
	"errors"
	"log"
	"web_backend/internal/authctx"
	"web_backend/internal/httpx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

const sessionLocalsKey = "session"

func SessionFrom(c *fiber.Ctx) *models.Session {
	session, _ := c.Locals(sessionLocalsKey).(*models.Session)
	return session
}

func (h *UserHandler) SessionAuth(c *fiber.Ctx) error {
	// DevAuth sudah mengisi identitas saat pengembangan/test.
	if authctx.UserID(c) != "" {
		return c.Next()
	}
	if h.DB == nil || Session == nil {
		return httpx.Error(c, fiber.StatusServiceUnavailable, "Database is not connected")
	}

	session, err := Session.Validate(c)
	if err != nil {
		if errors.Is(err, ErrNoSession) || errors.Is(err, ErrInvalidSession) || errors.Is(err, ErrExpiredSession) {
			return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		log.Printf("[session] validate failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
	}

	var user models.User
	if err := h.DB.First(&user, "id = ?", session.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = Session.Revoke(c)
			return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		log.Printf("[session] user lookup failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
	}

	c.Locals(authctx.UserIDKey, user.ID)
	c.Locals(authctx.IsAdminKey, user.IsAdmin)
	c.Locals(sessionLocalsKey, session)
	return c.Next()
}
