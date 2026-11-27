package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"web_backend/internal/api"
	"web_backend/internal/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GET /admin/audit hanya untuk Platform Admin dan mengembalikan log terbaru.
func TestAuditLogRequiresAdmin(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrator().DropTable(&models.AuditLog{}); err != nil {
		t.Fatalf("drop audit_logs: %v", err)
	}
	if err := db.AutoMigrate(&models.AuditLog{}); err != nil {
		t.Fatalf("migrate audit_logs: %v", err)
	}
	if err := db.Create(&models.AuditLog{ActorID: "actor-1", Action: "test.action", TargetType: "test", TargetID: "x"}).Error; err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	app := fiber.New()
	api.SetupRoutes(app, db, nil, true /* devAuth */, nil, nil)

	get := func(userID string, admin bool) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/admin/audit", nil)
		if userID != "" {
			req.Header.Set("X-Debug-User", userID)
		}
		if admin {
			req.Header.Set("X-Debug-Admin", "true")
		}
		res, err := app.Test(req, 15000)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return res.StatusCode, body
	}

	status, body := get(uuid.NewString(), true)
	if status != fiber.StatusOK {
		t.Fatalf("admin: status %d body %v", status, body)
	}
	logs, ok := body["logs"].([]any)
	if !ok || len(logs) != 1 {
		t.Fatalf("logs = %v, mau 1 baris", body["logs"])
	}
	first, _ := logs[0].(map[string]any)
	if first["action"] != "test.action" {
		t.Errorf("log = %v", first)
	}

	if status, _ := get(uuid.NewString(), false); status != fiber.StatusForbidden {
		t.Errorf("non-admin: status %d, mau 403", status)
	}
}
