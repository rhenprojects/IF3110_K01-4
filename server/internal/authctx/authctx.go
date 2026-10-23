package authctx

import (
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
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	return c.Next()
}

func RequireAdmin(c *fiber.Ctx) error {
	if UserID(c) == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	if !IsAdmin(c) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Platform admin only"})
	}
	return c.Next()
}
