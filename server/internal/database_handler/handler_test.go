package databasehandler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"web_backend/internal/authctx"
	"web_backend/internal/targetdb"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestQueryInt(t *testing.T) {
	cases := []struct {
		raw                string
		fallback, min, max int
		want               int
	}{
		{"", 50, 1, 200, 50},
		{"abc", 50, 1, 200, 50},
		{"10", 50, 1, 200, 10},
		{"0", 50, 1, 200, 1},
		{"9999", 50, 1, 200, 200},
		{"-3", 0, 0, 100, 0},
	}
	for _, tc := range cases {
		if got := queryInt(tc.raw, tc.fallback, tc.min, tc.max); got != tc.want {
			t.Errorf("queryInt(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestConstraintType(t *testing.T) {
	want := map[string]string{"p": "primary_key", "f": "foreign_key", "u": "unique", "c": "check", "x": "x"}
	for kind, name := range want {
		if got := constraintType(kind); got != name {
			t.Errorf("constraintType(%q) = %q, want %q", kind, got, name)
		}
	}
}

func TestBuildRowsQueryQuotesIdentifiers(t *testing.T) {
	got := buildRowsQuery(`sales`, `items"; DROP TABLE x; --`, []string{"id", "Weird Col"}, []string{"id"})
	want := `SELECT "id", "Weird Col" FROM "sales"."items""; DROP TABLE x; --" ORDER BY "id" LIMIT $1 OFFSET $2`
	if got != want {
		t.Errorf("query =\n%s\nwant\n%s", got, want)
	}
	if got := buildRowsQuery("public", "logs", []string{"msg"}, nil); strings.Contains(got, "ORDER BY") {
		t.Errorf("table without primary key should not be ordered: %s", got)
	}
}

func TestJSONValue(t *testing.T) {
	ts := time.Date(2026, 10, 24, 7, 30, 0, 0, time.FixedZone("WIB", 7*3600))
	uuidBytes := [16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"time in UTC", ts, "2026-10-24T00:30:00Z"},
		{"bytes", []byte("abc"), "abc"},
		{"uuid array", uuidBytes, "12345678-9abc-def0-1234-56789abcdef0"},
		{"uuid pgtype", pgtype.UUID{Bytes: uuidBytes, Valid: true}, "12345678-9abc-def0-1234-56789abcdef0"},
		{"numeric", pgtype.Numeric{Int: big.NewInt(1050), Exp: -2, Valid: true}, "10.50"},
		{"null numeric", pgtype.Numeric{}, nil},
		{"date", pgtype.Date{Time: ts, Valid: true}, "2026-10-24"},
		{"null text", pgtype.Text{}, nil},
		{"int passthrough", int64(7), int64(7)},
	}
	for _, tc := range cases {
		if got := jsonValue(tc.in); got != tc.want {
			t.Errorf("%s: jsonValue = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

func TestTranslateTargetError(t *testing.T) {
	cases := []struct {
		err  error
		want error
	}{
		{&pgconn.PgError{Code: "42501"}, ErrTargetPermission},
		{&pgconn.PgError{Code: "57014"}, ErrQueryTimeout},
		{&pgconn.PgError{Code: "42P01"}, ErrQueryFailed},
		{fmt.Errorf("wait: %w", context.DeadlineExceeded), ErrQueryTimeout},
		{ErrTableNotFound, ErrTableNotFound},
		{targetdb.ErrUnreachable, targetdb.ErrUnreachable},
	}
	for _, tc := range cases {
		if got := translateTargetError(tc.err); !errors.Is(got, tc.want) {
			t.Errorf("translateTargetError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
	if translateTargetError(nil) != nil {
		t.Error("nil should stay nil")
	}
}

func TestWriteErrorMapping(t *testing.T) {
	h := &Handler{}
	cases := []struct {
		err    error
		status int
	}{
		{&validationError{"sql", "required"}, fiber.StatusBadRequest},
		{ErrDatabaseNotFound, fiber.StatusNotFound},
		{ErrNoDatabaseAccess, fiber.StatusForbidden},
		{ErrEditAccessRequired, fiber.StatusForbidden},
		{ErrTableNotFound, fiber.StatusNotFound},
		{ErrMigrationNotFound, fiber.StatusNotFound},
		{ErrMigrationNotDraft, fiber.StatusConflict},
		{ErrMigrationRunning, fiber.StatusConflict},
		{fmt.Errorf("%w: raw", ErrTargetPermission), fiber.StatusForbidden},
		{fmt.Errorf("%w: raw", ErrQueryTimeout), fiber.StatusGatewayTimeout},
		{fmt.Errorf("%w: raw", ErrQueryFailed), fiber.StatusBadGateway},
		{fmt.Errorf("%w: dial tcp", targetdb.ErrUnreachable), fiber.StatusBadGateway},
		{fmt.Errorf("%w: auth", targetdb.ErrAuthFailed), fiber.StatusBadGateway},
		{fmt.Errorf("%w: slow", targetdb.ErrTimeout), fiber.StatusGatewayTimeout},
		{errors.New("secret internal detail"), fiber.StatusInternalServerError},
	}
	for _, tc := range cases {
		app := fiber.New()
		app.Get("/", func(c *fiber.Ctx) error { return h.writeError(c, tc.err) })
		resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != tc.status {
			t.Errorf("%v: status %d, want %d", tc.err, resp.StatusCode, tc.status)
		}
		for _, leak := range []string{"raw", "dial tcp", "secret internal detail"} {
			if strings.Contains(string(body), leak) {
				t.Errorf("%v: response leaks %q: %s", tc.err, leak, body)
			}
		}
	}
}

func TestParseMigrationInput(t *testing.T) {
	cases := []struct {
		body  string
		field string
	}{
		{`{"name":"add users","sql":"CREATE TABLE u (id int)"}`, ""},
		{`{"name":"  ","sql":"SELECT 1"}`, "name"},
		{`{"name":"` + strings.Repeat("n", 201) + `","sql":"SELECT 1"}`, "name"},
		{`{"name":"x","sql":"   "}`, "sql"},
		{`not json`, "name"},
	}
	for _, tc := range cases {
		app := fiber.New()
		var gotErr error
		app.Post("/", func(c *fiber.Ctx) error {
			_, _, gotErr = parseMigrationInput(c)
			return nil
		})
		req := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if _, err := app.Test(req); err != nil {
			t.Fatal(err)
		}
		var verr *validationError
		switch {
		case tc.field == "" && gotErr != nil:
			t.Errorf("%s: unexpected error %v", tc.body, gotErr)
		case tc.field != "" && (!errors.As(gotErr, &verr) || verr.Field != tc.field):
			t.Errorf("%s: err %v, want validation error on %q", tc.body, gotErr, tc.field)
		}
	}
}

func TestRoutesRequireLoginAndDatabase(t *testing.T) {
	app := fiber.New()
	NewHandler(nil, nil).RegisterRoutes(app, authctx.DevAuth(true))

	req := httptest.NewRequest("GET", "/databases", nil)
	if resp, _ := app.Test(req); resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("anonymous: status %d, want 401", resp.StatusCode)
	}

	req = httptest.NewRequest("GET", "/databases", nil)
	req.Header.Set("X-Debug-User", "user-1")
	if resp, _ := app.Test(req); resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Errorf("no platform db: status %d, want 503", resp.StatusCode)
	}
}

func TestLockForIsPerConnection(t *testing.T) {
	h := &Handler{}
	a1, a2, b := h.lockFor("a"), h.lockFor("a"), h.lockFor("b")
	if a1 != a2 {
		t.Error("same connection should share one lock")
	}
	if a1 == b {
		t.Error("different connections should not share a lock")
	}
	a1.Lock()
	if a2.TryLock() {
		t.Error("second migration on the same connection should be blocked")
	}
	if !b.TryLock() {
		t.Error("migration on another connection should not be blocked")
	}
	a1.Unlock()
	b.Unlock()
}
