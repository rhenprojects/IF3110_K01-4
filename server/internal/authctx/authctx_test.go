package authctx

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func newApp(devAuth bool) *fiber.App {
	app := fiber.New()
	app.Use(DevAuth(devAuth))
	app.Get("/admin", RequireAdmin, func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/user", RequireAuth, func(c *fiber.Ctx) error { return c.SendString(UserID(c)) })
	return app
}

func status(t *testing.T, app *fiber.App, path string, headers map[string]string) int {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp.StatusCode
}

func TestRequireAdmin(t *testing.T) {
	app := newApp(true)
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"anonymous", nil, fiber.StatusUnauthorized},
		{"regular user", map[string]string{"X-Debug-User": "u1"}, fiber.StatusForbidden},
		{"admin", map[string]string{"X-Debug-User": "u1", "X-Debug-Admin": "true"}, fiber.StatusOK},
	}
	for _, tc := range cases {
		if got := status(t, app, "/admin", tc.headers); got != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestRequireAuth(t *testing.T) {
	app := newApp(true)
	if got := status(t, app, "/user", nil); got != fiber.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", got)
	}
	if got := status(t, app, "/user", map[string]string{"X-Debug-User": "u1"}); got != fiber.StatusOK {
		t.Errorf("logged in: status = %d, want 200", got)
	}
}

func TestDevAuthDisabledIgnoresHeaders(t *testing.T) {
	app := newApp(false)
	headers := map[string]string{"X-Debug-User": "u1", "X-Debug-Admin": "true"}
	if got := status(t, app, "/admin", headers); got != fiber.StatusUnauthorized {
		t.Errorf("status = %d, want 401 when DevAuth is disabled", got)
	}
}
