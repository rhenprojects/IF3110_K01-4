package middleware

import (
	"net/http/httptest"
	"testing"

	"web_backend/internal/authctx"

	"github.com/gofiber/fiber/v2"
)

func sessionApp(identity string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if identity != "" {
			authctx.SetIdentity(c, identity, false)
		}
		return c.Next()
	})
	app.Get("/private", NewAuth(nil, nil).RequireSession, func(c *fiber.Ctx) error {
		return c.SendString(authctx.UserID(c))
	})
	return app
}

func TestRequireSessionTrustsDevIdentity(t *testing.T) {
	resp, err := sessionApp("dev-user").Test(httptest.NewRequest("GET", "/private", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRequireSessionFailsWithoutDatabase(t *testing.T) {
	resp, err := sessionApp("").Test(httptest.NewRequest("GET", "/private", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
