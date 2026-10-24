package httpx

import (
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
)

// Error writes the standard error envelope used by the JSON API.
func Error(c *fiber.Ctx, status int, message string) error {
	return c.Status(status).JSON(models.ErrorResponse{Error: message})
}

// ValidationError writes a 400 response with the offending field.
func ValidationError(c *fiber.Ctx, field, message string) error {
	return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: message, Field: field})
}
