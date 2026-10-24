package api

import (
	"web_backend/internal/authctx"
	grouphandler "web_backend/internal/group_handler"
	"web_backend/internal/httpx"
	"web_backend/internal/models"
	"web_backend/internal/targetdb"
	userhandler "web_backend/internal/user_handler"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func SetupRoutes(app *fiber.App, db *gorm.DB, connService *targetdb.Service, devAuth bool) {
	api := app.Group("/api/v1")

	api.Get("/health", func(c *fiber.Ctx) error {
		dbStatus := "connected"
		if db == nil {
			dbStatus = "disconnected"
		}
		return c.JSON(models.HealthResponse{Status: "ok", Database: dbStatus})
	})

	api.Post("/test", func(c *fiber.Ctx) error {
		var payload map[string]any
		if err := c.BodyParser(&payload); err != nil {
			return httpx.Error(c, fiber.StatusBadRequest, "Invalid JSON body")
		}

		dbStatus := "connected"
		if db == nil {
			dbStatus = "disconnected (pending schema/database setup)"
		}

		return c.JSON(models.EchoTestResponse{
			Status:   "ok",
			Message:  "Connection to port 9000 verified. JSON received successfully.",
			Database: dbStatus,
			Echo:     payload,
		})
	})

	api.Get("/test", func(c *fiber.Ctx) error {
		dbStatus := "connected"
		if db == nil {
			dbStatus = "disconnected (pending schema/database setup)"
		}
		return c.JSON(models.EchoTestResponse{
			Status:   "ok",
			Message:  "Connection to port 9000 verified.",
			Database: dbStatus,
		})
	})

	userHandler := userhandler.NewUserHandler(db)
	middleware := []fiber.Handler{
		authctx.DevAuth(devAuth),
		userHandler.SessionAuth,
		userHandler.RequireCSRF,
	}

	userHandler.RegisterRoutes(api, api, middleware...)

	connHandler := targetdb.NewHandler(connService)
	connHandler.RegisterRoutes(api, middleware...)
	connHandler.RegisterUserRoutes(api, middleware...)

	grouphandler.NewGroupHandler(db).RegisterRoutes(api, middleware...)
}
