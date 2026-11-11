package authctx

import (
	"github.com/gofiber/fiber/v2"
)

func DevAuth(enabled bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !enabled {
			return c.Next()
		}
		if userID := c.Get("X-Debug-User"); userID != "" {
			SetIdentity(c, userID, c.Get("X-Debug-Admin") == "true")
		}
		return c.Next()
	}
}
