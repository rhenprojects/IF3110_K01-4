package models

import (
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const oldStatusCheck = `CHECK (status IN ('draft','success','failed'))`

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	all := All()
	for i := len(all) - 1; i >= 0; i-- {
		if err := db.Migrator().DropTable(all[i]); err != nil {
			t.Fatalf("drop table %T: %v", all[i], err)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type fixture struct {
	db          *gorm.DB
	userID      string
	connID      string
	migrationID string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testDB(t)
	user := User{Username: "u1", Name: "U1", Password: "x"}
	conn := DBConnection{Name: "c1", Host: "h", DBName: "d", Username: "u", PasswordEnc: []byte("x")}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&conn).Error; err != nil {
		t.Fatal(err)
	}
	m := Migration{ConnectionID: conn.ID, Name: "m1", SQL: "SELECT 1", CreatedBy: user.ID}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	return &fixture{db: db, userID: user.ID, connID: conn.ID, migrationID: m.ID}
}

func (f *fixture) changeRequest(kind JobKind, status ChangeRequestStatus, rollbackOf *string) error {
	return f.db.Create(&ChangeRequest{
		ConnectionID: f.connID, MigrationID: f.migrationID, Kind: kind,
		Status: status, RollbackOf: rollbackOf, RequestedBy: f.userID,
	}).Error
}

func (f *fixture) setMigrationStatus(status MigrationStatus) error {
	return f.db.Exec(`UPDATE migrations SET status = ? WHERE id = ?`, status, f.migrationID).Error
}

func mustFail(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: seharusnya ditolak database, tapi berhasil", what)
	}
}

func TestMigration_AcceptsReviewStatuses(t *testing.T) {
	f := newFixture(t)
	for _, s := range []MigrationStatus{MigrationPendingReview, MigrationApproved, MigrationDraft, MigrationSuccess, MigrationFailed} {
		if err := f.setMigrationStatus(s); err != nil {
			t.Errorf("status %q harus diterima: %v", s, err)
		}
	}
	mustFail(t, f.setMigrationStatus("queued"), "status migration di luar alur review")
}

func TestMigrate_UpgradesOldStatusConstraint(t *testing.T) {
	f := newFixture(t)
	for _, stmt := range []string{
		`ALTER TABLE migrations DROP CONSTRAINT chk_migrations_status`,
		`ALTER TABLE migrations ALTER COLUMN status TYPE varchar(10)`,
		`ALTER TABLE migrations ADD CONSTRAINT chk_migrations_status ` + oldStatusCheck,
	} {
		if err := f.db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	mustFail(t, f.setMigrationStatus(MigrationPendingReview), "skema lama menolak pending_review")

	if err := Migrate(f.db); err != nil {
		t.Fatalf("migrate database lama: %v", err)
	}

	if err := f.setMigrationStatus(MigrationPendingReview); err != nil {
		t.Fatalf("setelah upgrade, pending_review harus diterima: %v", err)
	}
	var length int
	f.db.Raw(`SELECT character_maximum_length FROM information_schema.columns
		WHERE table_name = 'migrations' AND column_name = 'status'`).Scan(&length)
	if length != 20 {
		t.Errorf("panjang kolom status = %d, mau 20", length)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	db := testDB(t)
	for i := 0; i < 2; i++ {
		if err := Migrate(db); err != nil {
			t.Fatalf("migrate ke-%d: %v", i+2, err)
		}
	}
	var def string
	db.Raw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'chk_migrations_status'`).Scan(&def)
	if !strings.Contains(def, "pending_review") || !strings.Contains(def, "approved") {
		t.Errorf("constraint = %q", def)
	}
}

func TestChangeRequest_OnePendingPerMigration(t *testing.T) {
	f := newFixture(t)
	if err := f.changeRequest(JobKindUp, ChangeRequestPendingReview, nil); err != nil {
		t.Fatal(err)
	}
	mustFail(t, f.changeRequest(JobKindUp, ChangeRequestPendingReview, nil), "dua pending pada migration yang sama")

	if err := f.db.Exec(`UPDATE change_requests SET status = 'rejected' WHERE migration_id = ?`, f.migrationID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.changeRequest(JobKindUp, ChangeRequestPendingReview, nil); err != nil {
		t.Errorf("setelah ditolak, pengajuan baru harus boleh: %v", err)
	}
}

func TestChangeRequest_Constraints(t *testing.T) {
	f := newFixture(t)
	execID := "11111111-1111-1111-1111-111111111111"

	mustFail(t, f.changeRequest("bogus", ChangeRequestPendingReview, nil), "kind tidak dikenal")
	mustFail(t, f.changeRequest(JobKindUp, "bogus", nil), "status tidak dikenal")
	mustFail(t, f.changeRequest(JobKindRollback, ChangeRequestApproved, nil), "rollback tanpa rollback_of")
	mustFail(t, f.changeRequest(JobKindForward, ChangeRequestApproved, &execID), "forward dengan rollback_of")

	if err := f.changeRequest(JobKindRollback, ChangeRequestApproved, &execID); err != nil {
		t.Errorf("rollback dengan rollback_of harus diterima: %v", err)
	}
}

func TestApproval_OneDecisionPerApprover(t *testing.T) {
	f := newFixture(t)
	cr := ChangeRequest{ConnectionID: f.connID, MigrationID: f.migrationID, Kind: JobKindUp, RequestedBy: f.userID}
	if err := f.db.Create(&cr).Error; err != nil {
		t.Fatal(err)
	}
	approve := func(approver string, d ApprovalDecision) error {
		return f.db.Create(&Approval{ChangeRequestID: cr.ID, ApproverID: approver, Decision: d}).Error
	}

	if err := approve("leader-1", DecisionApprove); err != nil {
		t.Fatal(err)
	}
	mustFail(t, approve("leader-1", DecisionApprove), "approver yang sama dua kali")
	mustFail(t, approve("leader-2", "maybe"), "decision tidak dikenal")
	if err := approve("leader-2", DecisionReject); err != nil {
		t.Errorf("leader lain harus boleh memutuskan: %v", err)
	}
}

func TestGroup_Leader(t *testing.T) {
	f := newFixture(t)
	missing := "22222222-2222-2222-2222-222222222222"

	mustFail(t, f.db.Create(&Group{Name: "ghost", LeaderID: &missing}).Error, "leader yang bukan user")

	g := Group{Name: "g1", LeaderID: &f.userID}
	if err := f.db.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Unscoped().Delete(&User{}, "id = ?", f.userID).Error; err != nil {
		t.Fatal(err)
	}
	var got Group
	if err := f.db.First(&got, "id = ?", g.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.LeaderID != nil {
		t.Errorf("leader_id harus menjadi NULL setelah user dihapus, dapat %v", *got.LeaderID)
	}
}
