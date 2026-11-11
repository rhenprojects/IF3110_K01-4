package authctx

import (
	"web_backend/internal/httpx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
)

const (
	userIDKey  = "userID"
	isAdminKey = "isAdmin"
	sessionKey = "session"
)

// UserID mengembalikan ID user yang sudah terautentikasi, atau string kosong.
func UserID(c *fiber.Ctx) string {
	id, _ := c.Locals(userIDKey).(string)
	return id
}

// IsAdmin melaporkan apakah user yang terautentikasi adalah Platform Admin.
func IsAdmin(c *fiber.Ctx) bool {
	admin, _ := c.Locals(isAdminKey).(bool)
	return admin
}

// Session mengembalikan session yang sedang dipakai request ini, atau nil.
func Session(c *fiber.Ctx) *models.Session {
	session, _ := c.Locals(sessionKey).(*models.Session)
	return session
}

// SetIdentity menyimpan identitas user yang terautentikasi ke request context.
func SetIdentity(c *fiber.Ctx, userID string, isAdmin bool) {
	c.Locals(userIDKey, userID)
	c.Locals(isAdminKey, isAdmin)
}

// SetSession menyimpan session yang sudah divalidasi ke request context.
func SetSession(c *fiber.Ctx, session *models.Session) {
	c.Locals(sessionKey, session)
}

func RequireAuth(c *fiber.Ctx) error {
	if !ensureAuthenticated(c) {
		return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	return c.Next()
}

func RequireAdmin(c *fiber.Ctx) error {
	if !ensureAuthenticated(c) {
		return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	if !IsAdmin(c) {
		return httpx.Error(c, fiber.StatusForbidden, "Platform admin only")
	}
	return c.Next()
}

// ensureAuthenticated memisahkan pengecekan identitas dari penulisan response,
// sehingga RequireAuth dan RequireAdmin memakai aturan yang sama tanpa
// memanggil c.Next() lebih dari sekali.
func ensureAuthenticated(c *fiber.Ctx) bool {
	return UserID(c) != ""
}
