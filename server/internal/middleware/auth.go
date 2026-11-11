// Package middleware memasang autentikasi dan proteksi CSRF pada aplikasi Fiber.
//
// Rantai middleware dirakit sekali di api.SetupRoutes, sehingga paket handler
// tidak perlu (dan tidak bisa lupa) memasang proteksi sendiri. Urutan yang
// berlaku untuk setiap route terproteksi:
//
//	autentikasi (RequireSession) -> CSRF (RequireCSRF) -> ketersediaan resource -> otorisasi
package middleware

import (
	"errors"
	"log"

	"web_backend/internal/authctx"
	"web_backend/internal/httpx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// SessionStore adalah backend session yang dibutuhkan Auth.
// userhandler.SessionService memenuhi interface ini.
type SessionStore interface {
	Validate(c *fiber.Ctx) (*models.Session, error)
	Revoke(c *fiber.Ctx) error
	IsUnauthorized(err error) bool
}

// Auth mengautentikasi request dan menegakkan proteksi CSRF.
type Auth struct {
	db       *gorm.DB
	sessions SessionStore
}

func NewAuth(db *gorm.DB, sessions SessionStore) *Auth {
	return &Auth{db: db, sessions: sessions}
}

// RequireSession memvalidasi session cookie, memuat user, lalu menyimpan
// identitas user dan session ke request context. Request yang sudah diberi
// identitas oleh DevAuth (hanya saat pengembangan) melewati validasi session.
func (a *Auth) RequireSession(c *fiber.Ctx) error {
	if authctx.UserID(c) != "" {
		return c.Next()
	}
	if a.db == nil {
		return httpx.Error(c, fiber.StatusServiceUnavailable, "Database is not connected")
	}
	if a.sessions == nil {
		return httpx.Error(c, fiber.StatusServiceUnavailable, "Session service is not available")
	}

	session, err := a.sessions.Validate(c)
	if err != nil {
		if a.sessions.IsUnauthorized(err) {
			return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		log.Printf("[session] validate failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
	}

	var user models.User
	if err := a.db.First(&user, "id = ?", session.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = a.sessions.Revoke(c)
			return httpx.Error(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		log.Printf("[session] user lookup failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
	}

	authctx.SetIdentity(c, user.ID, user.IsAdmin)
	authctx.SetSession(c, session)
	return c.Next()
}
