package middleware

import "github.com/gofiber/fiber/v2"

// SecurityHeaders menambahkan header pertahanan dasar untuk semua respons.
// CSP tidak dipasang di API; frontend mengaturnya lewat nginx.
func SecurityHeaders(c *fiber.Ctx) error {
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("X-Frame-Options", "DENY")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	return c.Next()
}
