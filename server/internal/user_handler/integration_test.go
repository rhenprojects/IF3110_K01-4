package userhandler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"web_backend/internal/authctx"
	"web_backend/internal/models"
	userhandler "web_backend/internal/user_handler"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setup(t *testing.T) (*fiber.App, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrator().DropTable(&models.User{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	userhandler.InitSession(nil, nil)

	app := fiber.New()
	protected := app.Group("", authctx.DevAuth(true))
	userhandler.NewUserHandler(db).RegisterRoutes(protected)
	return app, db
}

func do(t *testing.T, app *fiber.App, method, path string, body any, headers map[string]string) (int, map[string]any, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, 15000)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(respBody, &out)
	return resp.StatusCode, out, string(respBody)
}

var asAdmin = map[string]string{"X-Debug-User": "admin-1", "X-Debug-Admin": "true"}

func TestIntegrationEnsureAdmin(t *testing.T) {
	_, db := setup(t)

	if _, err := userhandler.EnsureAdmin(db, "", ""); !errors.Is(err, userhandler.ErrNoAdminConfigured) {
		t.Fatalf("no admin, no env: err = %v, want ErrNoAdminConfigured", err)
	}
	created, err := userhandler.EnsureAdmin(db, "root", "rootpassword")
	if err != nil || !created {
		t.Fatalf("first EnsureAdmin: created=%v err=%v", created, err)
	}
	created, err = userhandler.EnsureAdmin(db, "other", "otherpassword")
	if err != nil || created {
		t.Fatalf("second EnsureAdmin: created=%v err=%v, want no-op", created, err)
	}

	var admin models.User
	if err := db.First(&admin, "username = ?", "root").Error; err != nil {
		t.Fatal(err)
	}
	if !admin.IsAdmin || admin.Password == "rootpassword" {
		t.Errorf("admin = %+v, want is_admin and hashed password", admin)
	}
}

func TestIntegrationLogin(t *testing.T) {
	app, db := setup(t)
	if _, err := userhandler.CreateUser(db, models.CreateUserRequest{Username: "alice", Password: "alice-password"}); err != nil {
		t.Fatal(err)
	}

	status, body, raw := do(t, app, "POST", "/users/login", map[string]string{"username": "alice", "password": "alice-password"}, nil)
	if status != fiber.StatusOK {
		t.Fatalf("login: status %d, body %s", status, raw)
	}
	if strings.Contains(raw, "alice-password") || strings.Contains(raw, "$2a$") {
		t.Errorf("login response leaks password or hash: %s", raw)
	}
	if user, _ := body["user"].(map[string]any); user["username"] != "alice" {
		t.Errorf("login user = %v", body["user"])
	}

	_, wrong, _ := do(t, app, "POST", "/users/login", map[string]string{"username": "alice", "password": "nope-nope"}, nil)
	status, unknown, _ := do(t, app, "POST", "/users/login", map[string]string{"username": "ghost", "password": "nope-nope"}, nil)
	if status != fiber.StatusUnauthorized || wrong["error"] != unknown["error"] {
		t.Errorf("wrong=%v unknown=%v status=%d, want identical 401", wrong, unknown, status)
	}
}

func TestIntegrationCreateUserByAdmin(t *testing.T) {
	app, _ := setup(t)
	newUser := map[string]any{"username": "bob", "password": "bob-password"}

	status, body, raw := do(t, app, "POST", "/admin/users", newUser, asAdmin)
	if status != fiber.StatusCreated || body["username"] != "bob" || body["is_admin"] != false {
		t.Fatalf("create: status %d, body %s", status, raw)
	}
	if strings.Contains(raw, "bob-password") || strings.Contains(raw, "password") {
		t.Errorf("create response leaks password: %s", raw)
	}

	if status, _, _ = do(t, app, "POST", "/admin/users", newUser, asAdmin); status != fiber.StatusConflict {
		t.Errorf("duplicate: status %d, want 409", status)
	}

	status, body, _ = do(t, app, "POST", "/admin/users", map[string]any{"username": "carol", "password": "short"}, asAdmin)
	if status != fiber.StatusBadRequest || body["field"] != "password" {
		t.Errorf("validation: status %d, body %v", status, body)
	}

	if status, _, raw = do(t, app, "POST", "/users/login", newUser, nil); status != fiber.StatusOK {
		t.Errorf("new user login: status %d, body %s", status, raw)
	}
}

func TestIntegrationCreateUserDenied(t *testing.T) {
	app, db := setup(t)
	newUser := map[string]any{"username": "mallory", "password": "mallory-password", "is_admin": true}

	if status, _, _ := do(t, app, "POST", "/admin/users", newUser, nil); status != fiber.StatusUnauthorized {
		t.Errorf("anonymous: status %d, want 401", status)
	}
	if status, _, _ := do(t, app, "POST", "/admin/users", newUser, map[string]string{"X-Debug-User": "u1"}); status != fiber.StatusForbidden {
		t.Errorf("regular user: status %d, want 403", status)
	}
	if status, _, _ := do(t, app, "POST", "/users/register", newUser, nil); status != fiber.StatusNotFound {
		t.Errorf("public register: status %d, want 404", status)
	}

	var count int64
	db.Model(&models.User{}).Where("username = ?", "mallory").Count(&count)
	if count != 0 {
		t.Error("denied request created a user")
	}
}
