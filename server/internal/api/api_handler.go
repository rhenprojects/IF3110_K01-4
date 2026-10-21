package api

import (
	userhandler "web_backend/internal/user_handler"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func SetupRoutes(app *fiber.App, db *gorm.DB) {
	api := app.Group("/api/v1")

	api.Get("/health", func(c *fiber.Ctx) error {
		dbStatus := "connected"
		if db == nil {
			dbStatus = "disconnected"
		}
		return c.JSON(fiber.Map{
			"status":   "ok",
			"database": dbStatus,
		})
	})

	// Test connection endpoint to receive and respond with JSON
	api.Post("/test", func(c *fiber.Ctx) error {
		var payload map[string]interface{}
		if err := c.BodyParser(&payload); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"status":  "error",
				"message": "Invalid JSON body",
			})
		}

		dbStatus := "connected"
		if db == nil {
			dbStatus = "disconnected (pending schema/database setup)"
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"status":   "ok",
			"message":  "Connection to port 9000 verified. JSON received successfully.",
			"database": dbStatus,
			"echo":     payload,
		})
	})

	api.Get("/test", func(c *fiber.Ctx) error {
		dbStatus := "connected"
		if db == nil {
			dbStatus = "disconnected (pending schema/database setup)"
		}
		return c.JSON(fiber.Map{
			"status":   "ok",
			"message":  "Connection to port 9000 verified.",
			"database": dbStatus,
		})
	})

	userHandler := userhandler.NewUserHandler(db)
	userHandler.RegisterRoutes(api)
}
