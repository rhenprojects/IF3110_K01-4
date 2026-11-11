package api

import (
	"web_backend/internal/authctx"
	databasehandler "web_backend/internal/database_handler"
	grouphandler "web_backend/internal/group_handler"
	"web_backend/internal/httpx"
	"web_backend/internal/middleware"
	"web_backend/internal/models"
	"web_backend/internal/targetdb"
	userhandler "web_backend/internal/user_handler"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

var benchRawBody = []byte(`{"status":"ok"}`)

func SetupRoutes(app *fiber.App, db *gorm.DB, connService *targetdb.Service, devAuth bool, sessions *userhandler.SessionService) {
	api := app.Group("/api/v1")

	// Route publik (health, echo, login) harus didaftarkan sebelum grup
	// protected, karena middleware protected berlaku untuk prefix /api/v1.
	api.Get("/bench-raw", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Send(benchRawBody)
	})

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

	userHandler := userhandler.NewUserHandler(db, sessions)
	userHandler.RegisterPublicRoutes(api)

	auth := middleware.NewAuth(db, sessions)
	protected := api.Group("",
		authctx.DevAuth(devAuth),
		auth.RequireSession,
		auth.RequireCSRF,
	)

	userHandler.RegisterRoutes(protected)

	connHandler := targetdb.NewHandler(connService)
	connHandler.RegisterRoutes(protected)
	connHandler.RegisterUserRoutes(protected)

	grouphandler.NewGroupHandler(db).RegisterRoutes(protected)
	dbHandler := databasehandler.NewHandler(db, connService)
	dbHandler.RegisterRoutes(protected)
	dbHandler.RegisterBatchRoutes(protected)
}
