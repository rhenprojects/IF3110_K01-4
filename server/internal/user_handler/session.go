package userhandler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

const (
	defaultIdleTimeout      = 30 * time.Minute
	defaultAbsoluteTimeout  = 12 * time.Hour
	defaultRotationInterval = 15 * time.Minute
	defaultGracePeriod      = 30 * time.Second
)

var (
	ErrNoSession      = errors.New("session not found")
	ErrInvalidSession = errors.New("invalid session")
	ErrExpiredSession = errors.New("session expired or revoked")
)

type SessionConfig struct {
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
}

func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		IdleTimeout:      defaultIdleTimeout,
		AbsoluteTimeout:  defaultAbsoluteTimeout,
		RotationInterval: defaultRotationInterval,
		GracePeriod:      defaultGracePeriod,
	}
}

type SessionService struct {
	DB            *gorm.DB
	cookie        *sessionCookie
	Config        SessionConfig
	SecureCookies bool
	Now           func() time.Time
}

var Session *SessionService

func NewSessionService(db *gorm.DB, hashKey, blockKey []byte) *SessionService {
	return &SessionService{
		DB:     db,
		cookie: newSessionCookie(hashKey, blockKey),
		Config: DefaultSessionConfig(),
		Now:    time.Now,
	}
}

func InitSession(db *gorm.DB, hashKey, blockKey []byte) *SessionService {
	Session = NewSessionService(db, hashKey, blockKey)
	return Session
}

func (s *SessionService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *SessionService) idleTimeout() time.Duration {
	if s.Config.IdleTimeout > 0 {
		return s.Config.IdleTimeout
	}
	return defaultIdleTimeout
}

func (s *SessionService) absoluteTimeout() time.Duration {
	if s.Config.AbsoluteTimeout > 0 {
		return s.Config.AbsoluteTimeout
	}
	return defaultAbsoluteTimeout
}

func (s *SessionService) rotationInterval() time.Duration {
	if s.Config.RotationInterval > 0 {
		return s.Config.RotationInterval
	}
	return defaultRotationInterval
}

func (s *SessionService) gracePeriod() time.Duration {
	if s.Config.GracePeriod > 0 {
		return s.Config.GracePeriod
	}
	return defaultGracePeriod
}

func generateToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (s *SessionService) Create(c *fiber.Ctx, userID string) (*models.Session, string, error) {
	if s == nil || s.DB == nil {
		return nil, "", errors.New("session storage is not available")
	}

	token, err := generateToken()
	if err != nil {
		return nil, "", err
	}
	csrfToken, err := generateToken()
	if err != nil {
		return nil, "", err
	}

	now := s.now()
	session := models.Session{
		UserID:            userID,
		TokenHash:         hashToken(token),
		CSRFToken:         csrfToken,
		CreatedAt:         now,
		LastSeenAt:        now,
		RotatedAt:         now,
		AbsoluteExpiresAt: now.Add(s.absoluteTimeout()),
	}
	if err := s.DB.Create(&session).Error; err != nil {
		return nil, "", err
	}
	if err := s.cookie.set(c, token, session.AbsoluteExpiresAt, s.SecureCookies); err != nil {
		return nil, "", err
	}
	s.cookie.setCSRF(c, csrfToken, session.AbsoluteExpiresAt, s.SecureCookies)
	return &session, csrfToken, nil
}

// Validate memeriksa cookie session, timeout, dan melakukan rotasi token.
// Token lama tetap diterima selama grace period setelah rotasi.
func (s *SessionService) Validate(c *fiber.Ctx) (*models.Session, error) {
	if s == nil || s.DB == nil {
		return nil, ErrInvalidSession
	}
	token, err := s.cookie.get(c)
	if err != nil {
		return nil, ErrNoSession
	}
	tokenHash := hashToken(token)
	now := s.now()

	var session models.Session
	matchedPrev := false
	err = s.DB.Where("token_hash = ?", tokenHash).First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		matchedPrev = true
		err = s.DB.
			Where("prev_token_hash = ? AND prev_valid_until IS NOT NULL AND prev_valid_until > ?", tokenHash, now).
			First(&session).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}

	if session.RevokedAt != nil {
		return nil, ErrExpiredSession
	}
	if !now.Before(session.AbsoluteExpiresAt) || now.Sub(session.LastSeenAt) > s.idleTimeout() {
		s.revokeSession(&session, now)
		return nil, ErrExpiredSession
	}

	rotate := matchedPrev || session.RotatedAt.IsZero() || now.Sub(session.RotatedAt) >= s.rotationInterval()
	var newToken string
	if rotate {
		newToken, err = generateToken()
		if err != nil {
			return nil, err
		}
		session.PrevTokenHash = session.TokenHash
		prevValidUntil := now.Add(s.gracePeriod())
		session.PrevValidUntil = &prevValidUntil
		session.TokenHash = hashToken(newToken)
		session.RotatedAt = now
	}
	session.LastSeenAt = now
	if err := s.DB.Save(&session).Error; err != nil {
		return nil, err
	}
	if rotate {
		if err := s.cookie.set(c, newToken, session.AbsoluteExpiresAt, s.SecureCookies); err != nil {
			return nil, err
		}
		s.cookie.setCSRF(c, session.CSRFToken, session.AbsoluteExpiresAt, s.SecureCookies)
	}

	return &session, nil
}

func (s *SessionService) revokeSession(session *models.Session, now time.Time) {
	session.RevokedAt = &now
	_ = s.DB.Save(session).Error
}

func (s *SessionService) Revoke(c *fiber.Ctx) error {
	if s == nil || s.DB == nil {
		return errors.New("session storage is not available")
	}
	token, err := s.cookie.get(c)
	if err == nil {
		now := s.now()
		hash := hashToken(token)
		if err := s.DB.Model(&models.Session{}).
			Where("token_hash = ? OR prev_token_hash = ?", hash, hash).
			Update("revoked_at", now).Error; err != nil {
			return err
		}
	}
	s.cookie.clear(c, s.SecureCookies)
	return nil
}
