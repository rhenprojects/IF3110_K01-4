package userhandler

import (
	"errors"
	"log"
	"net/http"
	"time"
	"web_backend/internal/authctx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

type UserHandler struct {
	DB *gorm.DB
}

func NewUserHandler(db *gorm.DB) *UserHandler {
	return &UserHandler{DB: db}
}

// CreateUser hanya untuk Platform Admin; tidak ada registrasi publik.
func (h *UserHandler) CreateUser(c *fiber.Ctx) error {
	if h.DB == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Database is not connected"})
	}

	var req models.CreateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	user, err := CreateUser(h.DB, req)
	if err != nil {
		var verr *ValidationError
		switch {
		case errors.As(err, &verr):
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": verr.Message, "field": verr.Field})
		case errors.Is(err, ErrDuplicateUsername):
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": ErrDuplicateUsername.Error()})
		}
		log.Printf("[user] create user failed: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Internal server error"})
	}

	return c.Status(fiber.StatusCreated).JSON(user)
}

func (h *UserHandler) Login(c *fiber.Ctx) error {
	if h.DB == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Database is not connected"})
	}

	var req models.LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	var user models.User
	if err := h.DB.Where("username = ?", req.Username).First(&user).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("[user] login lookup failed: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Internal server error"})
	}
	if !CheckPassword(user.Password, req.Password) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid username or password"})
	}

	if err := Session.SetSessionCookie(c, user.ID, 24*time.Hour); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create session"})
	}

	return c.JSON(fiber.Map{
		"message": "Login successful",
		"user":    user,
	})
}

func (h *UserHandler) GetProfile(c *fiber.Ctx) error {
	if h.DB == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Database is not connected"})
	}

	userID, err := Session.GetSessionUserID(c)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}

	var user models.User
	if err := h.DB.First(&user, "id = ?", userID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}

	return c.JSON(user)
}

// RegisterRoutes: router harus sudah melewati middleware auth (mengisi authctx)
// agar endpoint admin bisa memeriksa hak akses.
func (h *UserHandler) RegisterRoutes(router fiber.Router) {
	usersGroup := router.Group("/users")
	usersGroup.Post("/login", h.Login)
	usersGroup.Get("/profile", h.GetProfile)

	adminGroup := router.Group("/admin/users", authctx.RequireAdmin)
	adminGroup.Post("/", h.CreateUser)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie := &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
