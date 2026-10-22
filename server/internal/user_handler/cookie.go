package userhandler

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gorilla/securecookie"
)

type SessionManager struct {
	cookie *securecookie.SecureCookie
}

var Session *SessionManager

func NewSessionManager(hashKey, blockKey []byte) *SessionManager {
	if len(hashKey) == 0 {
		hashKey = securecookie.GenerateRandomKey(64)
	}
	if len(blockKey) == 0 {
		blockKey = securecookie.GenerateRandomKey(32)
	}

	return &SessionManager{
		cookie: securecookie.New(hashKey, blockKey),
	}
}

func InitSession(hashKey, blockKey []byte) *SessionManager {
	Session = NewSessionManager(hashKey, blockKey)
	return Session
}

func (s *SessionManager) SetSessionCookie(c *fiber.Ctx, userID string, duration time.Duration) error {
	encoded, err := s.cookie.Encode("session", userID)
	if err != nil {
		return err
	}

	c.Cookie(&fiber.Cookie{
		Name:     "session",
		Value:    encoded,
		Path:     "/",
		HTTPOnly: true,
		Secure:   false,
		SameSite: "Lax",
		Expires:  time.Now().Add(duration),
	})

	return nil
}

func (s *SessionManager) GetSessionUserID(c *fiber.Ctx) (string, error) {
	cookieVal := c.Cookies("session")
	if cookieVal == "" {
		return "", errors.New("session cookie not found")
	}

	var userID string
	if err := s.cookie.Decode("session", cookieVal, &userID); err != nil {
		return "", errors.New("invalid or expired session cookie")
	}

	return userID, nil
}
