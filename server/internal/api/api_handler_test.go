package api_test

import (
	"io"
	"net/http/httptest"
	"testing"
	"web_backend/internal/api"

	"github.com/gofiber/fiber/v2"
)

func TestBenchRaw(t *testing.T) {
	app := fiber.New()
	api.SetupRoutes(app, nil, nil, false, nil)

	res, err := app.Test(httptest.NewRequest("GET", "/api/v1/bench-raw", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want %d", res.StatusCode, fiber.StatusOK)
	}
	if ct := res.Header.Get("Content-Type"); ct != fiber.MIMEApplicationJSON {
		t.Errorf("content type = %q, want %q", ct, fiber.MIMEApplicationJSON)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", body, `{"status":"ok"}`)
	}
}
