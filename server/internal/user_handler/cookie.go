package userhandler

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gorilla/securecookie"
)

const (
	sessionCookieName = "session"
	csrfCookieName    = "csrf_token"
)

type sessionCookie struct {
	codec *securecookie.SecureCookie
}

func newSessionCookie(hashKey, blockKey []byte) *sessionCookie {
	if len(hashKey) == 0 {
		hashKey = securecookie.GenerateRandomKey(64)
	}
	if len(blockKey) == 0 {
		blockKey = securecookie.GenerateRandomKey(32)
	}
	return &sessionCookie{codec: securecookie.New(hashKey, blockKey)}
}

func (s *sessionCookie) set(c *fiber.Ctx, token string, expires time.Time) error {
	encoded, err := s.codec.Encode(sessionCookieName, token)
	if err != nil {
		return err
	}
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    encoded,
		Path:     "/",
		HTTPOnly: true,
		Secure:   false,
		SameSite: "Lax",
		Expires:  expires,
	})
	return nil
}

// setCSRF memasang token di cookie yang sengaja tidak HttpOnly agar JavaScript
// dapat membacanya dan mengirimnya lewat header X-CSRF-Token.
func (s *sessionCookie) setCSRF(c *fiber.Ctx, token string, expires time.Time) {
	c.Cookie(&fiber.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HTTPOnly: false,
		Secure:   false,
		SameSite: "Lax",
		Expires:  expires,
	})
}

func (s *sessionCookie) get(c *fiber.Ctx) (string, error) {
	encoded := c.Cookies(sessionCookieName)
	if encoded == "" {
		return "", errors.New("session cookie not found")
	}
	var token string
	if err := s.codec.Decode(sessionCookieName, encoded, &token); err != nil {
		return "", errors.New("invalid session cookie")
	}
	return token, nil
}

func (s *sessionCookie) clear(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   -1,
	})
	c.Cookie(&fiber.Cookie{
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/",
		HTTPOnly: false,
		SameSite: "Lax",
		MaxAge:   -1,
	})
}
