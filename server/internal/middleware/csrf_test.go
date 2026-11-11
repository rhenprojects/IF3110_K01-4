package middleware

import (
	"net/http/httptest"
	"testing"

	"web_backend/internal/authctx"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
)

func csrfApp(session *models.Session, devUserID string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if session != nil {
			authctx.SetSession(c, session)
		}
		if devUserID != "" {
			authctx.SetIdentity(c, devUserID, false)
		}
		return c.Next()
	})
	auth := NewAuth(nil, nil)
	app.Post("/write", auth.RequireCSRF, func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
	app.Get("/read", auth.RequireCSRF, func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
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
	app := csrfApp(&models.Session{CSRFToken: "expected-token"}, "")

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

func TestRequireCSRFSkipsDevAuthRequest(t *testing.T) {
	app := csrfApp(nil, "dev-user")
	if status := csrfStatus(t, app, "POST", "/write", ""); status != fiber.StatusNoContent {
		t.Errorf("dev request without session: status %d, want 204", status)
	}
}

func TestRequireCSRFRejectsAnonymous(t *testing.T) {
	app := csrfApp(nil, "")
	if status := csrfStatus(t, app, "POST", "/write", ""); status != fiber.StatusUnauthorized {
		t.Errorf("anonymous request without session: status %d, want 401", status)
	}
}
