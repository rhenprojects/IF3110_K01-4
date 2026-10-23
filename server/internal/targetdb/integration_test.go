package targetdb_test


import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"web_backend/internal/authctx"
	"web_backend/internal/models"
	"web_backend/internal/targetdb"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testEnv struct {
	app    *fiber.App
	db     *gorm.DB
	svc    *targetdb.Service
	target *pgconn.Config
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	target, err := pgconn.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse TEST_PG_DSN: %v", err)
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrator().DropTable(&models.DBConnection{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := db.AutoMigrate(&models.DBConnection{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cipher, err := targetdb.NewCipher(targetdb.RandomKey())
	if err != nil {
		t.Fatal(err)
	}
	pools := targetdb.NewPoolManager()
	t.Cleanup(pools.CloseAll)
	svc := targetdb.NewService(db, cipher, pools)

	app := fiber.New()
	app.Use(authctx.DevAuth(true))
	targetdb.NewHandler(svc).RegisterRoutes(app)
	return &testEnv{app: app, db: db, svc: svc, target: target}
}

type principal int

const (
	anonymous principal = iota
	regularUser
	admin
)

func (e *testEnv) do(t *testing.T, who principal, method, path string, body any) (int, map[string]any, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	switch who {
	case regularUser:
		req.Header.Set("X-Debug-User", "user-1")
	case admin:
		req.Header.Set("X-Debug-User", "admin-1")
		req.Header.Set("X-Debug-Admin", "true")
	}

	resp, err := e.app.Test(req, 15000)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

func (e *testEnv) validInput(name string) map[string]any {
	return map[string]any{
		"name":     name,
		"host":     e.target.Host,
		"port":     e.target.Port,
		"db_name":  e.target.Database,
		"username": e.target.User,
		"password": e.target.Password,
		"ssl_mode": "disable",
	}
}

func TestIntegrationConnectionLifecycle(t *testing.T) {
	e := setup(t)

	status, created, raw := e.do(t, admin, "POST", "/admin/connections", e.validInput("main"))
	if status != fiber.StatusCreated {
		t.Fatalf("create: status %d, body %s", status, raw)
	}
	if strings.Contains(raw, e.target.Password) || strings.Contains(raw, "password") {
		t.Errorf("create response leaks password: %s", raw)
	}
	id, _ := created["id"].(string)
	if created["created_by"] != "admin-1" {
		t.Errorf("created_by = %v, want admin-1", created["created_by"])
	}

	var stored models.DBConnection
	if err := e.db.First(&stored, "id = ?", id).Error; err != nil {
		t.Fatalf("load stored row: %v", err)
	}
	if bytes.Contains(stored.PasswordEnc, []byte(e.target.Password)) {
		t.Error("password stored in plaintext")
	}

	status, body, raw := e.do(t, admin, "POST", "/admin/connections/"+id+"/test", nil)
	if status != fiber.StatusOK || body["server_version"] == "" {
		t.Fatalf("test saved: status %d, body %s", status, raw)
	}

	status, body, _ = e.do(t, admin, "GET", "/admin/connections", nil)
	if list, _ := body["connections"].([]any); status != fiber.StatusOK || len(list) != 1 {
		t.Fatalf("list: status %d, body %v", status, body)
	}

	// Update tanpa password mempertahankan password lama.
	update := e.validInput("main-renamed")
	delete(update, "password")
	status, body, raw = e.do(t, admin, "PUT", "/admin/connections/"+id, update)
	if status != fiber.StatusOK || body["name"] != "main-renamed" {
		t.Fatalf("update: status %d, body %s", status, raw)
	}
	if status, _, raw = e.do(t, admin, "POST", "/admin/connections/"+id+"/test", nil); status != fiber.StatusOK {
		t.Fatalf("test after update: status %d, body %s", status, raw)
	}

	pool, err := e.svc.Pool(context.Background(), id)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	var one int
	if err := pool.QueryRow(context.Background(), "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("query via pool: %v", err)
	}

	if status, _, _ = e.do(t, admin, "DELETE", "/admin/connections/"+id, nil); status != fiber.StatusNoContent {
		t.Fatalf("delete: status %d", status)
	}
	if status, _, _ = e.do(t, admin, "GET", "/admin/connections/"+id, nil); status != fiber.StatusNotFound {
		t.Fatalf("get after delete: status %d, want 404", status)
	}
	if _, err := e.svc.Pool(context.Background(), id); err != targetdb.ErrNotFound {
		t.Fatalf("Pool after delete: err %v, want ErrNotFound", err)
	}

	// Nama koneksi yang sudah dihapus boleh dipakai lagi.
	if status, _, raw = e.do(t, admin, "POST", "/admin/connections", e.validInput("main-renamed")); status != fiber.StatusCreated {
		t.Fatalf("recreate deleted name: status %d, body %s", status, raw)
	}
}

func TestIntegrationDuplicateName(t *testing.T) {
	e := setup(t)
	e.do(t, admin, "POST", "/admin/connections", e.validInput("dup"))
	if status, _, raw := e.do(t, admin, "POST", "/admin/connections", e.validInput("dup")); status != fiber.StatusConflict {
		t.Fatalf("duplicate: status %d, body %s", status, raw)
	}
}

func TestIntegrationValidationError(t *testing.T) {
	e := setup(t)
	in := e.validInput("bad")
	in["ssl_mode"] = "sometimes"
	status, body, _ := e.do(t, admin, "POST", "/admin/connections", in)
	if status != fiber.StatusBadRequest || body["field"] != "ssl_mode" {
		t.Fatalf("status %d, body %v", status, body)
	}
}

func TestIntegrationConnectionFailures(t *testing.T) {
	e := setup(t)

	wrongPassword := e.validInput("x")
	wrongPassword["password"] = "definitely-wrong-password"

	missingDB := e.validInput("x")
	missingDB["db_name"] = "db_that_does_not_exist_42"

	unreachable := e.validInput("x")
	unreachable["host"] = "127.0.0.1"
	unreachable["port"] = 1

	cases := []struct {
		name  string
		input map[string]any
		want  error
	}{
		{"wrong password", wrongPassword, targetdb.ErrAuthFailed},
		{"missing database", missingDB, targetdb.ErrDatabaseNotFound},
		{"unreachable host", unreachable, targetdb.ErrUnreachable},
	}
	for _, tc := range cases {
		status, body, raw := e.do(t, admin, "POST", "/admin/connections/test", tc.input)
		if status != fiber.StatusBadGateway || body["error"] != tc.want.Error() {
			t.Errorf("%s: status %d, body %s; want 502 %q", tc.name, status, raw, tc.want)
		}
		if strings.Contains(raw, "definitely-wrong-password") {
			t.Errorf("%s: response leaks password: %s", tc.name, raw)
		}
	}

	// Koneksi tersimpan yang gagal juga ditangani tanpa membuat server error.
	status, created, _ := e.do(t, admin, "POST", "/admin/connections", wrongPassword)
	if status != fiber.StatusCreated {
		t.Fatalf("create with wrong password: status %d", status)
	}
	id, _ := created["id"].(string)
	if status, _, raw := e.do(t, admin, "POST", "/admin/connections/"+id+"/test", nil); status != fiber.StatusBadGateway {
		t.Errorf("test saved wrong password: status %d, body %s", status, raw)
	}
}

func TestIntegrationAccessDenied(t *testing.T) {
	e := setup(t)
	status, created, _ := e.do(t, admin, "POST", "/admin/connections", e.validInput("guarded"))
	if status != fiber.StatusCreated {
		t.Fatalf("setup create: status %d", status)
	}
	id, _ := created["id"].(string)

	requests := []struct{ method, path string }{
		{"GET", "/admin/connections"},
		{"POST", "/admin/connections"},
		{"POST", "/admin/connections/test"},
		{"GET", "/admin/connections/" + id},
		{"PUT", "/admin/connections/" + id},
		{"DELETE", "/admin/connections/" + id},
		{"POST", "/admin/connections/" + id + "/test"},
	}
	for _, r := range requests {
		if status, _, _ := e.do(t, anonymous, r.method, r.path, e.validInput("hacked")); status != fiber.StatusUnauthorized {
			t.Errorf("anonymous %s %s: status %d, want 401", r.method, r.path, status)
		}
		if status, _, _ := e.do(t, regularUser, r.method, r.path, e.validInput("hacked")); status != fiber.StatusForbidden {
			t.Errorf("regular user %s %s: status %d, want 403", r.method, r.path, status)
		}
	}

	// Pastikan request yang ditolak tidak mengubah apa pun.
	status, body, _ := e.do(t, admin, "GET", "/admin/connections/"+id, nil)
	if status != fiber.StatusOK || body["name"] != "guarded" {
		t.Errorf("connection changed by denied requests: status %d, body %v", status, body)
	}
}
