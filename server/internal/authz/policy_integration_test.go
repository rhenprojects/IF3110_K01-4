package authz_test

import (
	"context"
	"os"
	"testing"
	"web_backend/internal/authz"
	"web_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setup(t *testing.T) *gorm.DB {
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
		&models.Session{}, &models.GroupDatabaseAccess{}, &models.GroupMember{},
		&models.Group{}, &models.DBConnection{}, &models.User{},
	); err != nil {
		t.Fatalf("drop tables: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Group{}, &models.GroupMember{},
		&models.DBConnection{}, &models.GroupDatabaseAccess{}, &models.Session{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func createUser(t *testing.T, db *gorm.DB, username string, admin bool) *models.User {
	t.Helper()
	user := models.User{Username: username, Name: username, Password: "hash", IsAdmin: admin}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return &user
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

func grant(t *testing.T, db *gorm.DB, userID, connectionID string, level models.AccessLevel) {
	t.Helper()
	group := models.Group{Name: "group-" + uuid.NewString()}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Create(&models.GroupMember{GroupID: group.ID, UserID: userID}).Error; err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := db.Create(&models.GroupDatabaseAccess{
		GroupID: group.ID, ConnectionID: connectionID, Level: level,
	}).Error; err != nil {
		t.Fatalf("grant access: %v", err)
	}
}

func TestIntegrationLevelMatrix(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	admin := createUser(t, db, "admin", true)
	user := createUser(t, db, "alice", false)
	conn := createConnection(t, db, "analytics")

	level, err := authz.Level(ctx, db, user.ID, conn.ID)
	if err != nil || level != models.AccessNone {
		t.Fatalf("without access: level=%q err=%v, want none", level, err)
	}

	grant(t, db, user.ID, conn.ID, models.AccessView)
	level, err = authz.Level(ctx, db, user.ID, conn.ID)
	if err != nil || level != models.AccessView {
		t.Fatalf("view: level=%q err=%v, want view", level, err)
	}
	if ok, err := authz.CanView(ctx, db, user.ID, conn.ID); err != nil || !ok {
		t.Errorf("CanView(view) = %v, %v; want true", ok, err)
	}
	if ok, err := authz.CanEdit(ctx, db, user.ID, conn.ID); err != nil || ok {
		t.Errorf("CanEdit(view) = %v, %v; want false", ok, err)
	}

	grant(t, db, user.ID, conn.ID, models.AccessEdit)
	level, err = authz.Level(ctx, db, user.ID, conn.ID)
	if err != nil || level != models.AccessEdit {
		t.Fatalf("view+edit: level=%q err=%v, want edit", level, err)
	}
	if ok, err := authz.CanEdit(ctx, db, user.ID, conn.ID); err != nil || !ok {
		t.Errorf("CanEdit(view+edit) = %v, %v; want true", ok, err)
	}

	level, err = authz.Level(ctx, db, admin.ID, conn.ID)
	if err != nil || level != models.AccessNone {
		t.Errorf("admin without group: level=%q err=%v, want none", level, err)
	}
	grant(t, db, admin.ID, conn.ID, models.AccessView)
	level, err = authz.Level(ctx, db, admin.ID, conn.ID)
	if err != nil || level != models.AccessView {
		t.Errorf("admin via group: level=%q err=%v, want view", level, err)
	}

	for _, ids := range [][2]string{
		{"not-a-uuid", conn.ID},
		{user.ID, "not-a-uuid"},
		{uuid.NewString(), conn.ID},
	} {
		level, err = authz.Level(ctx, db, ids[0], ids[1])
		if err != nil || level != models.AccessNone {
			t.Errorf("invalid/unknown ids %v: level=%q err=%v, want none", ids, level, err)
		}
	}

	if err := db.Delete(&models.GroupMember{}, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("remove membership: %v", err)
	}
	level, err = authz.Level(ctx, db, user.ID, conn.ID)
	if err != nil || level != models.AccessNone {
		t.Errorf("after membership removal: level=%q err=%v, want none", level, err)
	}
}
