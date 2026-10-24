package authctx

import (
	"web_backend/internal/httpx"

	"github.com/gofiber/fiber/v2"
)

const (
	UserIDKey  = "userID"
	IsAdminKey = "isAdmin"
)

func UserID(c *fiber.Ctx) string {
	id, _ := c.Locals(UserIDKey).(string)
	return id
}

func IsAdmin(c *fiber.Ctx) bool {
	admin, _ := c.Locals(IsAdminKey).(bool)
	return admin
}

func RequireAuth(c *fiber.Ctx) error {
	if UserID(c) == "" {
		return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	return c.Next()
}

func RequireAdmin(c *fiber.Ctx) error {
	if UserID(c) == "" {
		return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	if !IsAdmin(c) {
		return httpx.Error(c, fiber.StatusForbidden, "Platform admin only")
	}
	return c.Next()
}
