package userhandler

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"web_backend/internal/models"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	minPasswordLength = 8
	maxPasswordBytes  = 72 // bcrypt mengabaikan byte setelah 72
)

var (
	usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,50}$`)

	ErrDuplicateUsername = errors.New("username already exists")
	ErrNoAdminConfigured = errors.New("no platform admin exists and ADMIN_USERNAME/ADMIN_PASSWORD are not set")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func ValidateCreateUser(req *models.CreateUserRequest) error {
	req.Username = strings.TrimSpace(req.Username)
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = req.Username
	}

	switch {
	case !usernamePattern.MatchString(req.Username):
		return &ValidationError{"username", "3-50 characters: letters, digits, '_', '.', '-'"}
	case len(req.Name) > 100:
		return &ValidationError{"name", "max 100 characters"}
	case len(req.Password) < minPasswordLength:
		return &ValidationError{"password", fmt.Sprintf("at least %d characters", minPasswordLength)}
	case len(req.Password) > maxPasswordBytes:
		return &ValidationError{"password", fmt.Sprintf("at most %d bytes", maxPasswordBytes)}
	}
	return nil
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

// CheckPassword membandingkan password dengan hash. Jika user tidak ditemukan
// (hash kosong), tetap menjalankan bcrypt terhadap hash dummy agar waktu respons
// tidak membocorkan username mana yang terdaftar.
func CheckPassword(hash, password string) bool {
	if hash == "" {
		dummyHashOnce.Do(func() {
			dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)
		})
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func CreateUser(db *gorm.DB, req models.CreateUserRequest) (*models.User, error) {
	if err := ValidateCreateUser(&req); err != nil {
		return nil, err
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	user := models.User{
		Username: req.Username,
		Name:     req.Name,
		Password: hash,
		IsAdmin:  req.IsAdmin,
	}
	if err := db.Create(&user).Error; err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateUsername
		}
		return nil, err
	}
	return &user, nil
}

// EnsureAdmin membuat Platform Admin pertama dari env jika belum ada admin sama sekali.
// Mengembalikan true jika admin baru dibuat.
func EnsureAdmin(db *gorm.DB, username, password string) (bool, error) {
	var count int64
	if err := db.Model(&models.User{}).Where("is_admin = ?", true).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if username == "" || password == "" {
		return false, ErrNoAdminConfigured
	}
	if _, err := CreateUser(db, models.CreateUserRequest{Username: username, Password: password, IsAdmin: true}); err != nil {
		return false, err
	}
	return true, nil
}
