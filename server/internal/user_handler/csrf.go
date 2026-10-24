package userhandler

import (
	"crypto/subtle"
	"strings"
	"web_backend/internal/httpx"

	"github.com/gofiber/fiber/v2"
)

const csrfHeaderName = "X-CSRF-Token"

// RequireCSRF mewajibkan token CSRF pada request yang mengubah data.
// Request development lewat DevAuth tidak memiliki session, sehingga dilewati.
func (h *UserHandler) RequireCSRF(c *fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return c.Next()
	}

	session := SessionFrom(c)
	if session == nil {
		return c.Next()
	}

	token := c.Get(csrfHeaderName)
	if token == "" {
		token = c.FormValue("csrf_token")
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(session.CSRFToken)) != 1 {
		return httpx.Error(c, fiber.StatusForbidden, "Invalid CSRF token")
	}
	return c.Next()
}
