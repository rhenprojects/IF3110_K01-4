package userhandler

import (
	"net/http/httptest"
	"testing"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
)

func csrfApp(session *models.Session) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if session != nil {
			c.Locals(sessionLocalsKey, session)
		}
		return c.Next()
	})
	handler := &UserHandler{}
	app.Post("/write", handler.RequireCSRF, func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
	app.Get("/read", handler.RequireCSRF, func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
	return app
}

func csrfStatus(t *testing.T, app *fiber.App, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set(csrfHeaderName, token)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp.StatusCode
}

func TestRequireCSRF(t *testing.T) {
	app := csrfApp(&models.Session{CSRFToken: "expected-token"})

	if status := csrfStatus(t, app, "POST", "/write", ""); status != fiber.StatusForbidden {
		t.Errorf("missing token: status %d, want 403", status)
	}
	if status := csrfStatus(t, app, "POST", "/write", "wrong-token"); status != fiber.StatusForbidden {
		t.Errorf("wrong token: status %d, want 403", status)
	}
	if status := csrfStatus(t, app, "POST", "/write", "expected-token"); status != fiber.StatusNoContent {
		t.Errorf("valid token: status %d, want 204", status)
	}
	if status := csrfStatus(t, app, "GET", "/read", ""); status != fiber.StatusNoContent {
		t.Errorf("safe method: status %d, want 204", status)
	}
}

func TestRequireCSRFSkipsWithoutSession(t *testing.T) {
	app := csrfApp(nil)
	if status := csrfStatus(t, app, "POST", "/write", ""); status != fiber.StatusNoContent {
		t.Errorf("dev request without session: status %d, want 204", status)
	}
}
