package userhandler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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
	if err := db.Migrator().DropTable(&models.Session{}, &models.User{}); err != nil {
		t.Fatalf("drop tables: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Session{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	userhandler.InitSession(db, nil, nil)

	h := userhandler.NewUserHandler(db)
	app := fiber.New()
	h.RegisterRoutes(app, app,
		authctx.DevAuth(true),
		h.SessionAuth,
		h.RequireCSRF,
	)
	return app, db
}

type apiResponse struct {
	Status  int
	Body    map[string]any
	Raw     string
	Cookies []*http.Cookie
}

func call(t *testing.T, app *fiber.App, method, path string, body any, headers map[string]string, cookies []*http.Cookie) apiResponse {
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
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := app.Test(req, 15000)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return apiResponse{Status: resp.StatusCode, Body: out, Raw: string(raw), Cookies: resp.Cookies()}
}

func replaceSessionCookie(previous []*http.Cookie, response apiResponse) []*http.Cookie {
	for _, cookie := range response.Cookies {
		if cookie.Name == "session" {
			return []*http.Cookie{cookie}
		}
	}
	return previous
}

func login(t *testing.T, app *fiber.App, username, password string) (apiResponse, []*http.Cookie, string) {
	t.Helper()
	resp := call(t, app, "POST", "/users/login", map[string]string{
		"username": username, "password": password,
	}, nil, nil)
	cookies := replaceSessionCookie(nil, resp)
	csrf := ""
	for _, cookie := range resp.Cookies {
		if cookie.Name == "csrf_token" {
			csrf = cookie.Value
			break
		}
	}
	return resp, cookies, csrf
}

func csrfHeaders(token string) map[string]string {
	return map[string]string{"X-CSRF-Token": token}
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

	resp, cookies, csrf := login(t, app, "alice", "alice-password")
	if resp.Status != fiber.StatusOK {
		t.Fatalf("login: status %d, body %s", resp.Status, resp.Raw)
	}
	if strings.Contains(resp.Raw, "alice-password") || strings.Contains(resp.Raw, "$2a$") {
		t.Errorf("login response leaks password or hash: %s", resp.Raw)
	}
	if strings.Contains(resp.Raw, "csrf_token") || strings.Contains(resp.Raw, "expires_at") {
		t.Errorf("login response leaks session metadata: %s", resp.Raw)
	}
	if user, _ := resp.Body["user"].(map[string]any); user["username"] != "alice" {
		t.Errorf("login user = %v", resp.Body["user"])
	}
	if csrf == "" || len(cookies) == 0 {
		t.Fatalf("login must set csrf and session cookies: cookies=%v body=%s", cookies, resp.Raw)
	}

	profile := call(t, app, "GET", "/users/profile", nil, nil, cookies)
	if profile.Status != fiber.StatusOK || !strings.Contains(profile.Raw, `"username":"alice"`) {
		t.Errorf("profile: status %d, body %s", profile.Status, profile.Raw)
	}

	if csrfEndpoint := call(t, app, "GET", "/users/csrf", nil, nil, cookies); csrfEndpoint.Status != fiber.StatusNotFound {
		t.Errorf("GET /users/csrf: status %d, want 404", csrfEndpoint.Status)
	}

	wrong := call(t, app, "POST", "/users/login", map[string]string{"username": "alice", "password": "nope-nope"}, nil, nil)
	unknown := call(t, app, "POST", "/users/login", map[string]string{"username": "ghost", "password": "nope-nope"}, nil, nil)
	if wrong.Status != fiber.StatusUnauthorized || unknown.Status != fiber.StatusUnauthorized ||
		wrong.Body["error"] != unknown.Body["error"] {
		t.Errorf("wrong=%v unknown=%v want identical 401", wrong.Body, unknown.Body)
	}
}

func TestIntegrationSessionRotationAndGrace(t *testing.T) {
	app, db := setup(t)
	user, err := userhandler.CreateUser(db, models.CreateUserRequest{Username: "alice", Password: "alice-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, cookies, _ := login(t, app, "alice", "alice-password")
	oldCookie := cookies[0].Value

	withinInterval := call(t, app, "GET", "/users/profile", nil, nil, cookies)
	if withinInterval.Status != fiber.StatusOK {
		t.Fatalf("profile: status %d, body %s", withinInterval.Status, withinInterval.Raw)
	}
	cookies = replaceSessionCookie(cookies, withinInterval)
	if cookies[0].Value != oldCookie {
		t.Fatal("session token rotated before the 15 minute interval")
	}

	if err := db.Model(&models.Session{}).
		Where("user_id = ?", user.ID).
		Update("rotated_at", time.Now().Add(-16*time.Minute)).Error; err != nil {
		t.Fatalf("age session: %v", err)
	}

	first := call(t, app, "GET", "/users/profile", nil, nil, cookies)
	if first.Status != fiber.StatusOK {
		t.Fatalf("profile: status %d, body %s", first.Status, first.Raw)
	}
	cookies = replaceSessionCookie(cookies, first)
	if cookies[0].Value == oldCookie {
		t.Fatal("session token was not rotated after the interval")
	}

	// Cookie lama masih diterima selama grace period dan client menerima token baru.
	grace := call(t, app, "GET", "/users/profile", nil, nil, []*http.Cookie{{Name: "session", Value: oldCookie}})
	if grace.Status != fiber.StatusOK {
		t.Errorf("old token during grace: status %d, body %s", grace.Status, grace.Raw)
	}
	graceCookies := replaceSessionCookie(nil, grace)
	if len(graceCookies) == 0 || graceCookies[0].Value == oldCookie {
		t.Error("request with grace token did not rotate the session again")
	}
}

func TestIntegrationSessionGraceExpires(t *testing.T) {
	app, db := setup(t)
	user, err := userhandler.CreateUser(db, models.CreateUserRequest{Username: "alice", Password: "alice-password"})
	if err != nil {
		t.Fatal(err)
	}

	_, cookies, _ := login(t, app, "alice", "alice-password")
	oldCookie := cookies[0].Value

	if err := db.Model(&models.Session{}).
		Where("user_id = ?", user.ID).
		Update("rotated_at", time.Now().Add(-16*time.Minute)).Error; err != nil {
		t.Fatalf("age session: %v", err)
	}

	first := call(t, app, "GET", "/users/profile", nil, nil, cookies)
	if first.Status != fiber.StatusOK {
		t.Fatalf("profile: status %d, body %s", first.Status, first.Raw)
	}

	if err := db.Model(&models.Session{}).
		Where("user_id = ?", user.ID).
		Update("prev_valid_until", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatalf("expire grace: %v", err)
	}

	expired := call(t, app, "GET", "/users/profile", nil, nil, []*http.Cookie{{Name: "session", Value: oldCookie}})
	if expired.Status != fiber.StatusUnauthorized {
		t.Errorf("old token after grace: status %d, want 401", expired.Status)
	}
}

func TestIntegrationSessionTimeout(t *testing.T) {
	app, db := setup(t)
	user, err := userhandler.CreateUser(db, models.CreateUserRequest{Username: "alice", Password: "alice-password"})
	if err != nil {
		t.Fatal(err)
	}

	_, cookies, _ := login(t, app, "alice", "alice-password")
	var session models.Session
	if err := db.Where("user_id = ?", user.ID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&session).Update("last_seen_at", time.Now().Add(-31*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}

	idle := call(t, app, "GET", "/users/profile", nil, nil, cookies)
	if idle.Status != fiber.StatusUnauthorized {
		t.Errorf("idle timeout: status %d, want 401", idle.Status)
	}
	var reloaded models.Session
	if err := db.First(&reloaded, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.RevokedAt == nil {
		t.Error("idle-expired session was not revoked")
	}

	_, cookies, _ = login(t, app, "alice", "alice-password")
	var second models.Session
	if err := db.Where("user_id = ?", user.ID).Order("created_at DESC").First(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&second).Update("absolute_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	absolute := call(t, app, "GET", "/users/profile", nil, nil, cookies)
	if absolute.Status != fiber.StatusUnauthorized {
		t.Errorf("absolute timeout: status %d, want 401", absolute.Status)
	}
}

func TestIntegrationLogout(t *testing.T) {
	app, db := setup(t)
	if _, err := userhandler.CreateUser(db, models.CreateUserRequest{Username: "alice", Password: "alice-password"}); err != nil {
		t.Fatal(err)
	}
	_, cookies, csrf := login(t, app, "alice", "alice-password")

	noCSRF := call(t, app, "POST", "/users/logout", nil, nil, cookies)
	if noCSRF.Status != fiber.StatusForbidden {
		t.Errorf("logout without csrf: status %d, want 403", noCSRF.Status)
	}
	cookies = replaceSessionCookie(cookies, noCSRF)

	logout := call(t, app, "POST", "/users/logout", nil, csrfHeaders(csrf), cookies)
	if logout.Status != fiber.StatusNoContent {
		t.Fatalf("logout: status %d, body %s", logout.Status, logout.Raw)
	}
	cookies = replaceSessionCookie(cookies, logout)

	profile := call(t, app, "GET", "/users/profile", nil, nil, cookies)
	if profile.Status != fiber.StatusUnauthorized {
		t.Errorf("profile after logout: status %d, want 401", profile.Status)
	}
}

func TestIntegrationCSRF(t *testing.T) {
	app, db := setup(t)
	if _, err := userhandler.EnsureAdmin(db, "root", "rootpassword"); err != nil {
		t.Fatal(err)
	}
	_, cookies, csrf := login(t, app, "root", "rootpassword")

	missing := call(t, app, "POST", "/admin/users", map[string]any{
		"username": "bob", "password": "bob-password",
	}, nil, cookies)
	if missing.Status != fiber.StatusForbidden {
		t.Errorf("admin write without csrf: status %d, want 403", missing.Status)
	}
	cookies = replaceSessionCookie(cookies, missing)

	ok := call(t, app, "POST", "/admin/users", map[string]any{
		"username": "bob", "password": "bob-password",
	}, csrfHeaders(csrf), cookies)
	if ok.Status != fiber.StatusCreated {
		t.Errorf("admin write with csrf: status %d, body %s", ok.Status, ok.Raw)
	}
}

func TestIntegrationUserCRUD(t *testing.T) {
	app, db := setup(t)
	if _, err := userhandler.EnsureAdmin(db, "root", "rootpassword"); err != nil {
		t.Fatal(err)
	}
	var root models.User
	if err := db.First(&root, "username = ?", "root").Error; err != nil {
		t.Fatal(err)
	}
	_, cookies, csrf := login(t, app, "root", "rootpassword")

	created := call(t, app, "POST", "/admin/users", map[string]any{
		"username": "bob", "name": "Bob", "password": "bob-password",
	}, csrfHeaders(csrf), cookies)
	if created.Status != fiber.StatusCreated {
		t.Fatalf("create: status %d, body %s", created.Status, created.Raw)
	}
	cookies = replaceSessionCookie(cookies, created)
	bobID, _ := created.Body["id"].(string)

	list := call(t, app, "GET", "/admin/users", nil, nil, cookies)
	cookies = replaceSessionCookie(cookies, list)
	if list.Status != fiber.StatusOK || !strings.Contains(list.Raw, `"username":"bob"`) {
		t.Errorf("list: status %d, body %s", list.Status, list.Raw)
	}
	get := call(t, app, "GET", "/admin/users/"+bobID, nil, nil, cookies)
	cookies = replaceSessionCookie(cookies, get)
	if get.Status != fiber.StatusOK || get.Body["name"] != "Bob" {
		t.Errorf("get: status %d, body %s", get.Status, get.Raw)
	}

	updated := call(t, app, "PUT", "/admin/users/"+bobID, map[string]any{
		"name": "Bobby", "password": "bob-new-password", "is_admin": true,
	}, csrfHeaders(csrf), cookies)
	cookies = replaceSessionCookie(cookies, updated)
	if updated.Status != fiber.StatusOK || updated.Body["name"] != "Bobby" || updated.Body["is_admin"] != true {
		t.Errorf("update: status %d, body %s", updated.Status, updated.Raw)
	}

	bobLogin, _, _ := login(t, app, "bob", "bob-new-password")
	if bobLogin.Status != fiber.StatusOK {
		t.Errorf("login with updated password: status %d", bobLogin.Status)
	}

	selfDelete := call(t, app, "DELETE", "/admin/users/"+root.ID, nil, csrfHeaders(csrf), cookies)
	cookies = replaceSessionCookie(cookies, selfDelete)
	if selfDelete.Status != fiber.StatusConflict {
		t.Errorf("delete self: status %d, want 409", selfDelete.Status)
	}

	deleted := call(t, app, "DELETE", "/admin/users/"+bobID, nil, csrfHeaders(csrf), cookies)
	cookies = replaceSessionCookie(cookies, deleted)
	if deleted.Status != fiber.StatusNoContent {
		t.Errorf("delete: status %d, body %s", deleted.Status, deleted.Raw)
	}
	after := call(t, app, "GET", "/admin/users/"+bobID, nil, nil, cookies)
	if after.Status != fiber.StatusNotFound {
		t.Errorf("get deleted: status %d, want 404", after.Status)
	}
}

func TestIntegrationSensitiveUserUpdatesRevokeSessions(t *testing.T) {
	app, db := setup(t)
	if _, err := userhandler.EnsureAdmin(db, "root", "rootpassword"); err != nil {
		t.Fatal(err)
	}
	bob, err := userhandler.CreateUser(db, models.CreateUserRequest{Username: "bob", Password: "bob-password"})
	if err != nil {
		t.Fatal(err)
	}
	_, adminCookies, adminCSRF := login(t, app, "root", "rootpassword")
	_, bobCookies, _ := login(t, app, "bob", "bob-password")

	updated := call(t, app, "PUT", "/admin/users/"+bob.ID, map[string]any{
		"password": "bob-new-password",
	}, csrfHeaders(adminCSRF), adminCookies)
	if updated.Status != fiber.StatusOK {
		t.Fatalf("update password: status %d, body %s", updated.Status, updated.Raw)
	}
	if profile := call(t, app, "GET", "/users/profile", nil, nil, bobCookies); profile.Status != fiber.StatusUnauthorized {
		t.Errorf("session after password change: status %d, want 401", profile.Status)
	}

	_, bobCookies, _ = login(t, app, "bob", "bob-new-password")
	promoted := call(t, app, "PUT", "/admin/users/"+bob.ID, map[string]any{
		"is_admin": true,
	}, csrfHeaders(adminCSRF), adminCookies)
	if promoted.Status != fiber.StatusOK {
		t.Fatalf("promote: status %d, body %s", promoted.Status, promoted.Raw)
	}
	if profile := call(t, app, "GET", "/users/profile", nil, nil, bobCookies); profile.Status != fiber.StatusUnauthorized {
		t.Errorf("session after admin change: status %d, want 401", profile.Status)
	}
}

func TestIntegrationCreateUserByAdmin(t *testing.T) {
	app, _ := setup(t)
	newUser := map[string]any{"username": "bob", "password": "bob-password"}

	resp := call(t, app, "POST", "/admin/users", newUser, asAdmin, nil)
	if resp.Status != fiber.StatusCreated || resp.Body["username"] != "bob" || resp.Body["is_admin"] != false {
		t.Fatalf("create: status %d, body %s", resp.Status, resp.Raw)
	}
	if strings.Contains(resp.Raw, "bob-password") || strings.Contains(resp.Raw, "password") {
		t.Errorf("create response leaks password: %s", resp.Raw)
	}

	if resp = call(t, app, "POST", "/admin/users", newUser, asAdmin, nil); resp.Status != fiber.StatusConflict {
		t.Errorf("duplicate: status %d, want 409", resp.Status)
	}

	resp = call(t, app, "POST", "/admin/users", map[string]any{"username": "carol", "password": "short"}, asAdmin, nil)
	if resp.Status != fiber.StatusBadRequest || resp.Body["field"] != "password" {
		t.Errorf("validation: status %d, body %v", resp.Status, resp.Body)
	}

	if resp = call(t, app, "POST", "/users/login", newUser, nil, nil); resp.Status != fiber.StatusOK {
		t.Errorf("new user login: status %d, body %s", resp.Status, resp.Raw)
	}
}

func TestIntegrationCreateUserDenied(t *testing.T) {
	app, db := setup(t)
	newUser := map[string]any{"username": "mallory", "password": "mallory-password", "is_admin": true}

	if resp := call(t, app, "POST", "/admin/users", newUser, nil, nil); resp.Status != fiber.StatusUnauthorized {
		t.Errorf("anonymous: status %d, want 401", resp.Status)
	}
	if resp := call(t, app, "POST", "/admin/users", newUser, map[string]string{"X-Debug-User": "u1"}, nil); resp.Status != fiber.StatusForbidden {
		t.Errorf("regular user: status %d, want 403", resp.Status)
	}
	if resp := call(t, app, "POST", "/users/register", newUser, nil, nil); resp.Status != fiber.StatusNotFound && resp.Status != fiber.StatusUnauthorized {
		t.Errorf("public register: status %d, want 404 or 401", resp.Status)
	}

	var count int64
	db.Model(&models.User{}).Where("username = ?", "mallory").Count(&count)
	if count != 0 {
		t.Error("denied request created a user")
	}
}
