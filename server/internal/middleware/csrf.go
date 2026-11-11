package middleware

import (
	"crypto/subtle"
	"strings"

	"web_backend/internal/authctx"
	"web_backend/internal/httpx"

	"github.com/gofiber/fiber/v2"
)

const csrfHeaderName = "X-CSRF-Token"

// RequireCSRF mewajibkan token CSRF pada request yang mengubah data.
// Middleware ini harus berjalan setelah RequireSession karena token CSRF
// disimpan pada session di request context.
func (a *Auth) RequireCSRF(c *fiber.Ctx) error {
	if isSafeMethod(c.Method()) {
		return c.Next()
	}

	session := authctx.Session(c)
	if session == nil {
		// Request DevAuth terautentikasi tanpa cookie session, sehingga tidak
		// ada token CSRF yang terikat. Selain jalur itu, sesi wajib sudah diisi
		// RequireSession; kalau tidak, tolak request.
		if authctx.UserID(c) != "" {
			return c.Next()
		}
		return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
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

func isSafeMethod(method string) bool {
	switch method {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return true
	}
	return false
}
