package grouphandler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"
	"web_backend/internal/authctx"
	"web_backend/internal/authz"
	grouphandler "web_backend/internal/group_handler"
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
	if err := db.Migrator().DropTable(
		&models.GroupDatabaseAccess{}, &models.GroupMember{}, &models.Group{},
		&models.DBConnection{}, &models.User{},
	); err != nil {
		t.Fatalf("drop tables: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Group{}, &models.GroupMember{},
		&models.DBConnection{}, &models.GroupDatabaseAccess{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	app := fiber.New()
	grouphandler.NewGroupHandler(db).RegisterRoutes(app, authctx.DevAuth(true))
	return app, db
}

var adminHeaders = map[string]string{"X-Debug-User": "admin-1", "X-Debug-Admin": "true"}

func do(t *testing.T, app *fiber.App, method, path string, body any, headers map[string]string) (int, map[string]any, string) {
	t.Helper()
	var rawBody []byte
	if body != nil {
		rawBody, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, 15000)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

func createUser(t *testing.T, db *gorm.DB, username string) *models.User {
	t.Helper()
	user, err := userhandler.CreateUser(db, models.CreateUserRequest{
		Username: username, Password: username + "-password",
	})
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return user
}

func createConnection(t *testing.T, db *gorm.DB, name string) *models.DBConnection {
	t.Helper()
	conn := models.DBConnection{
		Name: name, Host: "localhost", Port: 5432, DBName: "target",
		Username: "app", PasswordEnc: []byte{1, 2, 3}, SSLMode: "disable",
	}
	if err := db.Create(&conn).Error; err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return &conn
}

func TestIntegrationGroupCRUD(t *testing.T) {
	app, _ := setup(t)

	status, created, raw := do(t, app, "POST", "/admin/groups", map[string]any{
		"name": "engineering", "description": "Engineering team",
	}, adminHeaders)
	if status != fiber.StatusCreated {
		t.Fatalf("create: status %d, body %s", status, raw)
	}
	id, _ := created["id"].(string)
	if created["name"] != "engineering" {
		t.Errorf("created group = %v", created)
	}

	if status, _, raw = do(t, app, "POST", "/admin/groups", map[string]any{"name": "engineering"}, adminHeaders); status != fiber.StatusConflict {
		t.Errorf("duplicate: status %d, body %s", status, raw)
	}

	status, body, raw := do(t, app, "GET", "/admin/groups", nil, adminHeaders)
	if status != fiber.StatusOK || body["groups"] == nil {
		t.Errorf("list: status %d, body %s", status, raw)
	}

	status, body, _ = do(t, app, "GET", "/admin/groups/"+id, nil, adminHeaders)
	if status != fiber.StatusOK || body["description"] != "Engineering team" {
		t.Errorf("get: status %d, body %v", status, body)
	}

	status, body, raw = do(t, app, "PUT", "/admin/groups/"+id, map[string]any{"name": "platform"}, adminHeaders)
	if status != fiber.StatusOK || body["name"] != "platform" {
		t.Errorf("update: status %d, body %s", status, raw)
	}

	if status, _, _ = do(t, app, "DELETE", "/admin/groups/"+id, nil, adminHeaders); status != fiber.StatusNoContent {
		t.Errorf("delete: status %d, want 204", status)
	}
	if status, _, _ = do(t, app, "GET", "/admin/groups/"+id, nil, adminHeaders); status != fiber.StatusNotFound {
		t.Errorf("get deleted: status %d, want 404", status)
	}

	if status, _, _ = do(t, app, "GET", "/admin/groups", nil, nil); status != fiber.StatusUnauthorized {
		t.Errorf("anonymous: status %d, want 401", status)
	}
	if status, _, _ = do(t, app, "GET", "/admin/groups", nil, map[string]string{"X-Debug-User": "u1"}); status != fiber.StatusForbidden {
		t.Errorf("regular user: status %d, want 403", status)
	}
}

func TestIntegrationMembership(t *testing.T) {
	app, db := setup(t)
	_, group, _ := do(t, app, "POST", "/admin/groups", map[string]any{"name": "engineering"}, adminHeaders)
	groupID, _ := group["id"].(string)
	user := createUser(t, db, "alice")

	status, member, raw := do(t, app, "POST", "/admin/groups/"+groupID+"/members", map[string]any{"user_id": user.ID}, adminHeaders)
	if status != fiber.StatusCreated || member["username"] != "alice" {
		t.Fatalf("add member: status %d, body %s", status, raw)
	}
	if status, _, _ = do(t, app, "POST", "/admin/groups/"+groupID+"/members", map[string]any{"user_id": user.ID}, adminHeaders); status != fiber.StatusConflict {
		t.Errorf("duplicate member: status %d, want 409", status)
	}

	status, body, raw := do(t, app, "GET", "/admin/groups/"+groupID+"/members", nil, adminHeaders)
	if status != fiber.StatusOK || !bytes.Contains([]byte(raw), []byte("alice")) || body["members"] == nil {
		t.Errorf("list members: status %d, body %s", status, raw)
	}

	if status, _, _ = do(t, app, "DELETE", "/admin/groups/"+groupID+"/members/"+user.ID, nil, adminHeaders); status != fiber.StatusNoContent {
		t.Errorf("remove member: status %d, want 204", status)
	}
	if status, _, _ = do(t, app, "DELETE", "/admin/groups/"+groupID+"/members/"+user.ID, nil, adminHeaders); status != fiber.StatusNotFound {
		t.Errorf("remove again: status %d, want 404", status)
	}
}

func TestIntegrationGroupAccessAndAuthorization(t *testing.T) {
	app, db := setup(t)
	_, group, _ := do(t, app, "POST", "/admin/groups", map[string]any{"name": "engineering"}, adminHeaders)
	groupID, _ := group["id"].(string)
	user := createUser(t, db, "alice")
	conn := createConnection(t, db, "analytics")

	if status, _, _ := do(t, app, "POST", "/admin/groups/"+groupID+"/members", map[string]any{"user_id": user.ID}, adminHeaders); status != fiber.StatusCreated {
		t.Fatalf("add member: status %d", status)
	}

	status, access, raw := do(t, app, "PUT", "/admin/groups/"+groupID+"/connections/"+conn.ID, map[string]any{"level": "view"}, adminHeaders)
	if status != fiber.StatusOK || access["level"] != "view" {
		t.Fatalf("set view: status %d, body %s", status, raw)
	}
	canView, err := authz.CanView(context.Background(), db, user.ID, conn.ID)
	if err != nil || !canView {
		t.Errorf("CanView = %v, err = %v; want true", canView, err)
	}
	canEdit, err := authz.CanEdit(context.Background(), db, user.ID, conn.ID)
	if err != nil || canEdit {
		t.Errorf("CanEdit = %v, err = %v; want false", canEdit, err)
	}

	if status, _, _ = do(t, app, "PUT", "/admin/groups/"+groupID+"/connections/"+conn.ID, map[string]any{"level": "edit"}, adminHeaders); status != fiber.StatusOK {
		t.Fatalf("set edit: status %d", status)
	}
	canEdit, err = authz.CanEdit(context.Background(), db, user.ID, conn.ID)
	if err != nil || !canEdit {
		t.Errorf("CanEdit = %v, err = %v; want true", canEdit, err)
	}

	status, body, raw := do(t, app, "GET", "/admin/groups/"+groupID+"/connections", nil, adminHeaders)
	if status != fiber.StatusOK || !bytes.Contains([]byte(raw), []byte("analytics")) || body["access"] == nil {
		t.Errorf("list access: status %d, body %s", status, raw)
	}

	if status, _, _ = do(t, app, "DELETE", "/admin/groups/"+groupID+"/members/"+user.ID, nil, adminHeaders); status != fiber.StatusNoContent {
		t.Fatalf("remove member: status %d", status)
	}
	canView, err = authz.CanView(context.Background(), db, user.ID, conn.ID)
	if err != nil || canView {
		t.Errorf("CanView after membership removal = %v, err = %v; want false", canView, err)
	}

	if status, _, _ = do(t, app, "DELETE", "/admin/groups/"+groupID+"/connections/"+conn.ID, nil, adminHeaders); status != fiber.StatusNoContent {
		t.Errorf("remove access: status %d, want 204", status)
	}
}
