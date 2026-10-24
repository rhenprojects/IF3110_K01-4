package userhandler

import (
	"errors"
	"log"
	"web_backend/internal/authctx"
	"web_backend/internal/httpx"
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

func (h *UserHandler) requireDB(c *fiber.Ctx) error {
	if h.DB == nil {
		return httpx.Error(c, fiber.StatusServiceUnavailable, "Database is not connected")
	}
	return c.Next()
}

// CreateUser hanya untuk Platform Admin; tidak ada registrasi publik.
func (h *UserHandler) CreateUser(c *fiber.Ctx) error {
	var req models.CreateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}

	user, err := CreateUser(h.DB, req)
	if err != nil {
		return writeUserError(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(user)
}

func (h *UserHandler) Login(c *fiber.Ctx) error {
	var req models.LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}

	var user models.User
	if err := h.DB.Where("username = ?", req.Username).First(&user).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("[user] login lookup failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
	}
	if !CheckPassword(user.Password, req.Password) {
		return httpx.Error(c, fiber.StatusUnauthorized, "Invalid username or password")
	}

	if _, _, err := Session.Create(c, user.ID); err != nil {
		log.Printf("[user] create session failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Failed to create session")
	}

	return c.JSON(models.LoginResponse{
		Message: "Login successful",
		User:    user,
	})
}

func (h *UserHandler) Logout(c *fiber.Ctx) error {
	if err := Session.Revoke(c); err != nil {
		log.Printf("[user] logout failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Failed to logout")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *UserHandler) GetProfile(c *fiber.Ctx) error {
	user, err := GetUser(h.DB, authctx.UserID(c))
	if err != nil {
		return writeUserError(c, err)
	}
	return c.JSON(user)
}

func (h *UserHandler) ListUsers(c *fiber.Ctx) error {
	var users []models.User
	if err := h.DB.Order("username").Find(&users).Error; err != nil {
		log.Printf("[user] list failed: %v", err)
		return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
	}
	return c.JSON(models.UserListResponse{Users: users})
}

func (h *UserHandler) GetUserByID(c *fiber.Ctx) error {
	user, err := GetUser(h.DB, c.Params("id"))
	if err != nil {
		return writeUserError(c, err)
	}
	return c.JSON(user)
}

func (h *UserHandler) UpdateUser(c *fiber.Ctx) error {
	var req models.UpdateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.Error(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if sameUserID(c.Params("id"), authctx.UserID(c)) && req.IsAdmin != nil && !*req.IsAdmin {
		return httpx.Error(c, fiber.StatusConflict, "cannot remove your own admin status")
	}
	user, err := UpdateUser(h.DB, c.Params("id"), req)
	if err != nil {
		return writeUserError(c, err)
	}
	return c.JSON(user)
}

func (h *UserHandler) DeleteUser(c *fiber.Ctx) error {
	if err := DeleteUser(h.DB, c.Params("id"), authctx.UserID(c)); err != nil {
		return writeUserError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func writeUserError(c *fiber.Ctx, err error) error {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		return httpx.ValidationError(c, verr.Field, verr.Message)
	case errors.Is(err, ErrDuplicateUsername):
		return httpx.Error(c, fiber.StatusConflict, ErrDuplicateUsername.Error())
	case errors.Is(err, ErrUserNotFound):
		return httpx.Error(c, fiber.StatusNotFound, ErrUserNotFound.Error())
	case errors.Is(err, ErrLastAdmin):
		return httpx.Error(c, fiber.StatusConflict, ErrLastAdmin.Error())
	case errors.Is(err, ErrCannotDeleteSelf):
		return httpx.Error(c, fiber.StatusConflict, ErrCannotDeleteSelf.Error())
	}
	log.Printf("[user] internal error: %v", err)
	return httpx.Error(c, fiber.StatusInternalServerError, "Internal server error")
}

// RegisterRoutes mendaftarkan route publik dan route yang sudah dilindungi.
// Middleware diberikan sebagai argumen agar tidak berlaku ke route login.
func (h *UserHandler) RegisterRoutes(public, protected fiber.Router, middleware ...fiber.Handler) {
	usersPublic := public.Group("/users")
	usersPublic.Post("/login", h.requireDB, h.Login)

	protectedMiddleware := func(extra ...fiber.Handler) []fiber.Handler {
		all := make([]fiber.Handler, 0, len(middleware)+len(extra))
		all = append(all, middleware...)
		all = append(all, extra...)
		return all
	}

	users := protected.Group("/users", protectedMiddleware(h.requireDB)...)
	users.Get("/profile", h.GetProfile)
	users.Post("/logout", h.Logout)

	admin := protected.Group("/admin/users", protectedMiddleware(h.requireDB, authctx.RequireAdmin)...)
	admin.Post("/", h.CreateUser)
	admin.Get("/", h.ListUsers)
	admin.Get("/:id", h.GetUserByID)
	admin.Put("/:id", h.UpdateUser)
	admin.Delete("/:id", h.DeleteUser)
}
