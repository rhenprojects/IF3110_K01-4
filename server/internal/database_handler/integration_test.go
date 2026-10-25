package databasehandler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"web_backend/internal/authctx"
	databasehandler "web_backend/internal/database_handler"
	"web_backend/internal/models"
	"web_backend/internal/targetdb"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type env struct {
	t            *testing.T
	app          *fiber.App
	db           *gorm.DB
	dsn          string
	conns        *targetdb.Service
	schema       string
	connectionID string
	editor       string // akses View + Edit
	viewer       string // akses View Only
	outsider     string // tanpa akses

	userIDs, groupIDs, connectionIDs []string
}

func setup(t *testing.T) *env {
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
	if err := db.Migrator().DropTable(
		&models.Session{}, &models.Migration{}, &models.GroupDatabaseAccess{}, &models.GroupMember{},
		&models.Group{}, &models.DBConnection{}, &models.User{},
	); err != nil {
		t.Fatalf("drop tables: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Group{}, &models.GroupMember{}, &models.Session{},
		&models.DBConnection{}, &models.GroupDatabaseAccess{}, &models.Migration{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cipher, err := targetdb.NewCipher(targetdb.RandomKey())
	if err != nil {
		t.Fatal(err)
	}
	pools := targetdb.NewPoolManager()
	t.Cleanup(pools.CloseAll)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	e := &env{t: t, db: db, dsn: dsn, conns: targetdb.NewService(db, cipher, pools), schema: "dbh_test_" + suffix}
	t.Cleanup(e.cleanup)
	e.createFixture()

	e.connectionID = e.createConnection(targetdb.ConnectionInput{
		Name: "dbh-" + suffix, Host: target.Host, Port: int(target.Port), DBName: target.Database,
		Username: target.User, Password: target.Password, SSLMode: "disable",
	})
	e.editor = e.createUser("editor-" + suffix)
	e.viewer = e.createUser("viewer-" + suffix)
	e.outsider = e.createUser("outsider-" + suffix)
	e.grant(e.editor, e.connectionID, models.AccessEdit)
	e.grant(e.viewer, e.connectionID, models.AccessView)

	e.app = fiber.New()
	databasehandler.NewHandler(db, e.conns).RegisterRoutes(e.app, authctx.DevAuth(true))
	return e
}

func (e *env) createFixture() {
	s := pgx.Identifier{e.schema}.Sanitize()
	e.execTarget(
		`CREATE SCHEMA `+s,
		`CREATE TABLE `+s+`.customers (id serial PRIMARY KEY, email varchar(120) NOT NULL UNIQUE, name text)`,
		`CREATE TABLE `+s+`.orders (
			id bigserial PRIMARY KEY,
			customer_id int NOT NULL REFERENCES `+s+`.customers(id),
			total numeric(10,2) NOT NULL DEFAULT 0 CHECK (total >= 0),
			note text,
			ref uuid
		)`,
		`CREATE INDEX orders_customer_idx ON `+s+`.orders (customer_id)`,
		`CREATE VIEW `+s+`.order_summary AS SELECT customer_id, count(*) AS n FROM `+s+`.orders GROUP BY customer_id`,
		`CREATE TABLE `+s+`."Order Items" ("Line No" int PRIMARY KEY, label text)`,
		`INSERT INTO `+s+`.customers (email, name) VALUES ('a@x.io', 'Ana'), ('b@x.io', NULL)`,
		// Disisipkan tidak berurutan untuk membuktikan data diurutkan berdasarkan primary key.
		`INSERT INTO `+s+`.orders (id, customer_id, total, note, ref) VALUES
			(3, 2, 5, 'third', NULL),
			(1, 1, 10.50, 'first', '12345678-9abc-def0-1234-56789abcdef0'),
			(2, 1, 20, NULL, NULL)`,
		`INSERT INTO `+s+`."Order Items" VALUES (1, 'widget')`,
	)
}

func (e *env) execTarget(statements ...string) {
	e.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.dsn)
	if err != nil {
		e.t.Fatalf("connect target: %v", err)
	}
	defer conn.Close(ctx)
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			e.t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

func (e *env) targetTableExists(table string) bool {
	e.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.dsn)
	if err != nil {
		e.t.Fatalf("connect target: %v", err)
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, pgx.Identifier{e.schema, table}.Sanitize()).Scan(&exists); err != nil {
		e.t.Fatal(err)
	}
	return exists
}

func (e *env) cleanup() {
	if conn, err := pgx.Connect(context.Background(), e.dsn); err == nil {
		_, _ = conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+pgx.Identifier{e.schema}.Sanitize()+` CASCADE`)
		conn.Close(context.Background())
	}
	if len(e.connectionIDs) > 0 {
		e.db.Where("connection_id IN ?", e.connectionIDs).Delete(&models.Migration{})
		e.db.Where("connection_id IN ?", e.connectionIDs).Delete(&models.GroupDatabaseAccess{})
		e.db.Unscoped().Where("id IN ?", e.connectionIDs).Delete(&models.DBConnection{})
	}
	if len(e.groupIDs) > 0 {
		e.db.Where("group_id IN ?", e.groupIDs).Delete(&models.GroupMember{})
		e.db.Where("id IN ?", e.groupIDs).Delete(&models.Group{})
	}
	if len(e.userIDs) > 0 {
		e.db.Unscoped().Where("id IN ?", e.userIDs).Delete(&models.User{})
	}
}

func (e *env) createConnection(in targetdb.ConnectionInput) string {
	e.t.Helper()
	conn, err := e.conns.Create(context.Background(), in, "test")
	if err != nil {
		e.t.Fatalf("create connection: %v", err)
	}
	e.connectionIDs = append(e.connectionIDs, conn.ID)
	return conn.ID
}

func (e *env) createUser(username string) string {
	e.t.Helper()
	user := models.User{Username: username, Name: "Name " + username, Password: "x"}
	if err := e.db.Create(&user).Error; err != nil {
		e.t.Fatalf("create user: %v", err)
	}
	e.userIDs = append(e.userIDs, user.ID)
	return user.ID
}

func (e *env) grant(userID, connectionID string, level models.AccessLevel) {
	e.t.Helper()
	group := models.Group{Name: "group-" + userID + "-" + connectionID}
	if err := e.db.Create(&group).Error; err != nil {
		e.t.Fatalf("create group: %v", err)
	}
	e.groupIDs = append(e.groupIDs, group.ID)
	if err := e.db.Create(&models.GroupMember{GroupID: group.ID, UserID: userID}).Error; err != nil {
		e.t.Fatalf("add member: %v", err)
	}
	if err := e.db.Create(&models.GroupDatabaseAccess{GroupID: group.ID, ConnectionID: connectionID, Level: level}).Error; err != nil {
		e.t.Fatalf("grant access: %v", err)
	}
}

func (e *env) do(userID, method, path string, body any, out any) (int, string) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("X-Debug-User", userID)
	}
	resp, err := e.app.Test(req, 15000)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("decode %s: %v (%s)", path, err, raw)
		}
	}
	return resp.StatusCode, string(raw)
}

func (e *env) dbPath(parts ...string) string {
	p := "/databases/" + e.connectionID
	for _, part := range parts {
		p += "/" + url.PathEscape(part)
	}
	return p
}

func (e *env) createMigration(userID, name, sql string) models.Migration {
	e.t.Helper()
	var m models.Migration
	status, raw := e.do(userID, "POST", e.dbPath("migrations"), map[string]string{"name": name, "sql": sql}, &m)
	if status != fiber.StatusCreated {
		e.t.Fatalf("create migration: status %d body %s", status, raw)
	}
	return m
}

func (e *env) execute(userID, migrationID string) (int, models.Migration, string) {
	e.t.Helper()
	var m models.Migration
	status, raw := e.do(userID, "POST", e.dbPath("migrations", migrationID, "execute"), nil, &m)
	return status, m, raw
}


func TestIntegrationDatabaseList(t *testing.T) {
	e := setup(t)
	levels := func(userID string) map[string]models.AccessLevel {
		var resp models.DatabaseListResponse
		if status, raw := e.do(userID, "GET", "/databases", nil, &resp); status != fiber.StatusOK {
			t.Fatalf("list: status %d body %s", status, raw)
		}
		out := map[string]models.AccessLevel{}
		for _, d := range resp.Databases {
			out[d.ID] = d.Level
		}
		return out
	}
	if got := levels(e.editor)[e.connectionID]; got != models.AccessEdit {
		t.Errorf("editor level = %q, want edit", got)
	}
	if got := levels(e.viewer)[e.connectionID]; got != models.AccessView {
		t.Errorf("viewer level = %q, want view", got)
	}
	if _, ok := levels(e.outsider)[e.connectionID]; ok {
		t.Error("outsider should not see the database")
	}
}

func TestIntegrationSchema(t *testing.T) {
	e := setup(t)
	var resp models.SchemaResponse
	status, raw := e.do(e.viewer, "GET", e.dbPath("schema"), nil, &resp)
	if status != fiber.StatusOK {
		t.Fatalf("status %d body %s", status, raw)
	}

	tables := map[string]models.SchemaTable{}
	for _, s := range resp.Schemas {
		if s.Name == "pg_catalog" || s.Name == "information_schema" {
			t.Errorf("system schema %q should be hidden", s.Name)
		}
		if s.Name == e.schema {
			for _, tbl := range s.Tables {
				tables[tbl.Name] = tbl
			}
		}
	}
	for name, typ := range map[string]string{"customers": "table", "orders": "table", "order_summary": "view", "Order Items": "table"} {
		if tables[name].Type != typ {
			t.Errorf("table %q type = %q, want %q", name, tables[name].Type, typ)
		}
	}

	orders := tables["orders"]
	columns := map[string]models.SchemaColumn{}
	for _, c := range orders.Columns {
		columns[c.Name] = c
	}
	if c := columns["id"]; !c.IsPrimaryKey || c.Nullable {
		t.Errorf("id column = %+v", c)
	}
	if c := columns["total"]; c.Type != "numeric(10,2)" || c.Nullable || c.Default == nil || *c.Default != "0" {
		t.Errorf("total column = %+v", c)
	}
	if c := columns["note"]; !c.Nullable || c.Default != nil || c.IsPrimaryKey {
		t.Errorf("note column = %+v", c)
	}

	constraintTypes := map[string]bool{}
	for _, c := range orders.Constraints {
		constraintTypes[c.Type] = true
	}
	for _, typ := range []string{"primary_key", "foreign_key", "check"} {
		if !constraintTypes[typ] {
			t.Errorf("orders missing %s constraint: %+v", typ, orders.Constraints)
		}
	}
	hasUnique := false
	for _, c := range tables["customers"].Constraints {
		hasUnique = hasUnique || c.Type == "unique"
	}
	if !hasUnique {
		t.Errorf("customers missing unique constraint: %+v", tables["customers"].Constraints)
	}

	indexes := map[string]models.SchemaIndex{}
	for _, idx := range orders.Indexes {
		indexes[idx.Name] = idx
	}
	if idx, ok := indexes["orders_customer_idx"]; !ok || idx.IsUnique || !strings.Contains(idx.Definition, "customer_id") {
		t.Errorf("orders_customer_idx = %+v", idx)
	}
	if !indexes["orders_pkey"].IsUnique {
		t.Errorf("orders_pkey should be unique: %+v", indexes["orders_pkey"])
	}
}

func TestIntegrationRows(t *testing.T) {
	e := setup(t)
	var page1 models.RowsResponse
	status, raw := e.do(e.viewer, "GET", e.dbPath("tables", e.schema, "orders", "rows")+"?limit=2", nil, &page1)
	if status != fiber.StatusOK {
		t.Fatalf("status %d body %s", status, raw)
	}
	if page1.Total != 3 || len(page1.Rows) != 2 || page1.Limit != 2 || page1.Offset != 0 {
		t.Fatalf("page1 = %+v", page1)
	}
	if fmt.Sprint(page1.Rows[0][0]) != "1" || fmt.Sprint(page1.Rows[1][0]) != "2" {
		t.Errorf("rows not ordered by primary key: %v", page1.Rows)
	}
	if page1.Rows[0][2] != "10.50" {
		t.Errorf("numeric = %#v, want \"10.50\"", page1.Rows[0][2])
	}
	if page1.Rows[0][4] != "12345678-9abc-def0-1234-56789abcdef0" {
		t.Errorf("uuid = %#v, want uuid string", page1.Rows[0][4])
	}
	if page1.Rows[1][3] != nil {
		t.Errorf("NULL note = %#v, want null", page1.Rows[1][3])
	}
	if len(page1.Columns) != 5 || page1.Columns[2].Name != "total" || page1.Columns[2].Type != "numeric(10,2)" {
		t.Errorf("columns = %+v", page1.Columns)
	}

	var page2 models.RowsResponse
	e.do(e.viewer, "GET", e.dbPath("tables", e.schema, "orders", "rows")+"?limit=2&offset=2", nil, &page2)
	if len(page2.Rows) != 1 || fmt.Sprint(page2.Rows[0][0]) != "3" {
		t.Errorf("page2 = %+v", page2.Rows)
	}

	var spaced models.RowsResponse
	if status, raw := e.do(e.viewer, "GET", e.dbPath("tables", e.schema, "Order Items", "rows"), nil, &spaced); status != fiber.StatusOK || len(spaced.Rows) != 1 || spaced.Rows[0][1] != "widget" {
		t.Errorf("table with spaces: status %d body %s", status, raw)
	}

	var view models.RowsResponse
	if status, raw := e.do(e.viewer, "GET", e.dbPath("tables", e.schema, "order_summary", "rows"), nil, &view); status != fiber.StatusOK || view.Total != 2 {
		t.Errorf("view rows: status %d body %s", status, raw)
	}
}

func TestIntegrationRowsRejectsUnknownAndInjectedTables(t *testing.T) {
	e := setup(t)
	for _, table := range []string{"no_such_table", `orders"; DROP TABLE customers; --`, "customers WHERE 1=1"} {
		if status, raw := e.do(e.viewer, "GET", e.dbPath("tables", e.schema, table, "rows"), nil, nil); status != fiber.StatusNotFound {
			t.Errorf("table %q: status %d body %s, want 404", table, status, raw)
		}
	}
	if !e.targetTableExists("customers") {
		t.Error("customers table was dropped by an injection attempt")
	}
}

func TestIntegrationViewerAccessControl(t *testing.T) {
	e := setup(t)
	paths := []string{e.dbPath(), e.dbPath("schema"), e.dbPath("tables", e.schema, "orders", "rows")}
	for _, p := range paths {
		if status, _ := e.do("", "GET", p, nil, nil); status != fiber.StatusUnauthorized {
			t.Errorf("anonymous %s: status %d, want 401", p, status)
		}
		if status, _ := e.do(e.outsider, "GET", p, nil, nil); status != fiber.StatusForbidden {
			t.Errorf("outsider %s: status %d, want 403", p, status)
		}
		if status, raw := e.do(e.viewer, "GET", p, nil, nil); status != fiber.StatusOK {
			t.Errorf("viewer %s: status %d body %s, want 200", p, status, raw)
		}
	}
}

func TestIntegrationUnreachableTarget(t *testing.T) {
	e := setup(t)
	downID := e.createConnection(targetdb.ConnectionInput{
		Name: "down-" + e.viewer, Host: "127.0.0.1", Port: 1, DBName: "x", Username: "x", Password: "secret-pass", SSLMode: "disable",
	})
	e.grant(e.viewer, downID, models.AccessView)
	status, raw := e.do(e.viewer, "GET", "/databases/"+downID+"/schema", nil, nil)
	if status != fiber.StatusBadGateway || strings.Contains(raw, "secret-pass") {
		t.Errorf("unreachable target: status %d body %s, want 502 without secrets", status, raw)
	}
}


func TestIntegrationMigrationLifecycle(t *testing.T) {
	e := setup(t)
	s := pgx.Identifier{e.schema}.Sanitize()

	draft := e.createMigration(e.editor, "add products", `CREATE TABLE `+s+`.products (id int PRIMARY KEY)`)
	if draft.Status != models.MigrationDraft || draft.Version != nil || draft.CreatedBy != e.editor || draft.ExecutedAt != nil {
		t.Fatalf("draft = %+v", draft)
	}

	var updated models.Migration
	status, raw := e.do(e.editor, "PUT", e.dbPath("migrations", draft.ID),
		map[string]string{"name": "add products table", "sql": `CREATE TABLE ` + s + `.products (id int PRIMARY KEY, name text NOT NULL)`}, &updated)
	if status != fiber.StatusOK || updated.Name != "add products table" || !strings.Contains(updated.SQL, "name text") {
		t.Fatalf("update draft: status %d body %s", status, raw)
	}

	status, done, raw := e.execute(e.editor, draft.ID)
	if status != fiber.StatusOK || done.Status != models.MigrationSuccess || done.Version == nil || *done.Version != 1 {
		t.Fatalf("execute: status %d body %s", status, raw)
	}
	if done.ExecutedBy == nil || *done.ExecutedBy != e.editor || done.ExecutedAt == nil || done.DurationMs == nil || done.Error != "" {
		t.Errorf("execution not recorded: %+v", done)
	}
	if done.CreatedByName == "" || done.ExecutedByName == nil || *done.ExecutedByName == "" {
		t.Errorf("user names not attached: %+v", done)
	}
	if !e.targetTableExists("products") {
		t.Fatal("products table was not created in the target database")
	}

	// Migration yang sudah dijalankan tidak bisa diubah atau dijalankan ulang.
	if status, _ := e.do(e.editor, "PUT", e.dbPath("migrations", draft.ID), map[string]string{"name": "x", "sql": "SELECT 1"}, nil); status != fiber.StatusConflict {
		t.Errorf("update executed migration: status %d, want 409", status)
	}
	if status, _, _ := e.execute(e.editor, draft.ID); status != fiber.StatusConflict {
		t.Errorf("re-execute: status %d, want 409", status)
	}

	second := e.createMigration(e.editor, "seed products", `INSERT INTO `+s+`.products VALUES (1, 'pen')`)
	if _, m, raw := e.execute(e.editor, second.ID); m.Status != models.MigrationSuccess || m.Version == nil || *m.Version != 2 {
		t.Errorf("second migration should be version 2: %s", raw)
	}

	// Riwayat: semua migration, terbaru dulu, lengkap dengan versi.
	var history models.MigrationListResponse
	if status, raw := e.do(e.viewer, "GET", e.dbPath("migrations"), nil, &history); status != fiber.StatusOK || len(history.Migrations) != 2 {
		t.Fatalf("history: status %d body %s", status, raw)
	}
	if history.Migrations[0].ID != second.ID || *history.Migrations[1].Version != 1 {
		t.Errorf("history order = %s, %s", history.Migrations[0].Name, history.Migrations[1].Name)
	}

	var detail models.Migration
	if status, raw := e.do(e.viewer, "GET", e.dbPath("migrations", draft.ID), nil, &detail); status != fiber.StatusOK || detail.SQL != updated.SQL {
		t.Errorf("detail: status %d body %s", status, raw)
	}
}

func TestIntegrationMigrationRollbackOnFailure(t *testing.T) {
	e := setup(t)
	s := pgx.Identifier{e.schema}.Sanitize()

	// Statement pertama valid, statement kedua gagal: seluruh migration harus di-rollback.
	broken := e.createMigration(e.editor, "half broken",
		`CREATE TABLE `+s+`.half_done (id int); INSERT INTO `+s+`.does_not_exist VALUES (1);`)
	status, failed, raw := e.execute(e.editor, broken.ID)
	if status != fiber.StatusOK || failed.Status != models.MigrationFailed || failed.Version != nil {
		t.Fatalf("failed migration: status %d body %s", status, raw)
	}
	if !strings.Contains(failed.Error, "does_not_exist") || failed.ExecutedBy == nil || failed.ExecutedAt == nil {
		t.Errorf("failure not recorded: %+v", failed)
	}
	if e.targetTableExists("half_done") {
		t.Fatal("half_done exists: failed migration was not rolled back")
	}

	// Migration gagal tidak memakai nomor versi; versi tetap linear.
	ok := e.createMigration(e.editor, "works", `CREATE TABLE `+s+`.after_failure (id int)`)
	if _, m, raw := e.execute(e.editor, ok.ID); m.Version == nil || *m.Version != 1 {
		t.Errorf("version after failure should be 1: %s", raw)
	}
	if status, _, _ := e.execute(e.editor, broken.ID); status != fiber.StatusConflict {
		t.Errorf("re-executing failed migration: status %d, want 409", status)
	}
}

func TestIntegrationMigrationAccessControl(t *testing.T) {
	e := setup(t)
	s := pgx.Identifier{e.schema}.Sanitize()
	draft := e.createMigration(e.editor, "guarded", `CREATE TABLE `+s+`.guarded (id int)`)
	body := map[string]string{"name": "hacked", "sql": `DROP TABLE ` + s + `.customers`}

	writes := []struct{ method, path string }{
		{"POST", e.dbPath("migrations")},
		{"PUT", e.dbPath("migrations", draft.ID)},
		{"POST", e.dbPath("migrations", draft.ID, "execute")},
	}
	for _, w := range writes {
		if status, _ := e.do("", w.method, w.path, body, nil); status != fiber.StatusUnauthorized {
			t.Errorf("anonymous %s %s: status %d, want 401", w.method, w.path, status)
		}
		if status, _ := e.do(e.viewer, w.method, w.path, body, nil); status != fiber.StatusForbidden {
			t.Errorf("view-only %s %s: status %d, want 403", w.method, w.path, status)
		}
		if status, _ := e.do(e.outsider, w.method, w.path, body, nil); status != fiber.StatusForbidden {
			t.Errorf("outsider %s %s: status %d, want 403", w.method, w.path, status)
		}
	}

	// View-only tetap boleh melihat riwayat dan detail; outsider tidak.
	for _, p := range []string{e.dbPath("migrations"), e.dbPath("migrations", draft.ID)} {
		if status, _ := e.do(e.viewer, "GET", p, nil, nil); status != fiber.StatusOK {
			t.Errorf("view-only GET %s: status %d, want 200", p, status)
		}
		if status, _ := e.do(e.outsider, "GET", p, nil, nil); status != fiber.StatusForbidden {
			t.Errorf("outsider GET %s: status %d, want 403", p, status)
		}
	}

	// Tidak ada yang berubah akibat request yang ditolak.
	var still models.Migration
	e.do(e.editor, "GET", e.dbPath("migrations", draft.ID), nil, &still)
	if still.Status != models.MigrationDraft || still.Name != "guarded" {
		t.Errorf("draft changed by denied requests: %+v", still)
	}
	if e.targetTableExists("guarded") || !e.targetTableExists("customers") {
		t.Error("target database changed by denied requests")
	}
}

func TestIntegrationMigrationValidationAndNotFound(t *testing.T) {
	e := setup(t)
	if status, raw := e.do(e.editor, "POST", e.dbPath("migrations"), map[string]string{"name": "", "sql": "SELECT 1"}, nil); status != fiber.StatusBadRequest || !strings.Contains(raw, `"field":"name"`) {
		t.Errorf("empty name: status %d body %s", status, raw)
	}
	if status, raw := e.do(e.editor, "POST", e.dbPath("migrations"), map[string]string{"name": "x", "sql": " "}, nil); status != fiber.StatusBadRequest || !strings.Contains(raw, `"field":"sql"`) {
		t.Errorf("empty sql: status %d body %s", status, raw)
	}
	if status, _ := e.do(e.editor, "GET", e.dbPath("migrations", "not-a-uuid"), nil, nil); status != fiber.StatusNotFound {
		t.Errorf("bad migration id: status %d, want 404", status)
	}
	if status, _ := e.do(e.editor, "GET", "/databases/00000000-0000-0000-0000-000000000000/migrations", nil, nil); status != fiber.StatusNotFound {
		t.Errorf("unknown database: status %d, want 404", status)
	}

	// Migration milik database lain tidak bisa diakses lewat database ini.
	otherID := e.createConnection(targetdb.ConnectionInput{
		Name: "other-" + e.editor, Host: "127.0.0.1", Port: 1, DBName: "x", Username: "x", Password: "x", SSLMode: "disable",
	})
	e.grant(e.editor, otherID, models.AccessEdit)
	var other models.Migration
	e.do(e.editor, "POST", "/databases/"+otherID+"/migrations", map[string]string{"name": "other", "sql": "SELECT 1"}, &other)
	if status, _ := e.do(e.editor, "GET", e.dbPath("migrations", other.ID), nil, nil); status != fiber.StatusNotFound {
		t.Errorf("cross-database migration: status %d, want 404", status)
	}
}
