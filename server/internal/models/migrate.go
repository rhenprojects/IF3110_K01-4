package models

import (
	"strings"

	"gorm.io/gorm"
)

// Migrate membuat/menyesuaikan seluruh tabel platform, lalu memasang trigger yang membuat migration_executions append-only
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(All()...); err != nil {
		return err
	}
	if err := upgradeMigrationStatus(db); err != nil {
		return err
	}
	if err := upgradeJobKind(db); err != nil {
		return err
	}
	if err := upgradeChangeRequestKind(db); err != nil {
		return err
	}
	statements := []string{
		`CREATE OR REPLACE FUNCTION dbmp_reject_history_change() RETURNS trigger
	LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''migration_executions is append-only''; END;'`,
		`DROP TRIGGER IF EXISTS trg_migration_executions_append_only ON migration_executions`,
		`CREATE TRIGGER trg_migration_executions_append_only
	BEFORE UPDATE OR DELETE ON migration_executions
	FOR EACH ROW EXECUTE FUNCTION dbmp_reject_history_change()`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

func upgradeJobKind(db *gorm.DB) error {
	var def string
	if err := db.Raw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conname = 'chk_migration_jobs_kind' AND conrelid = 'migration_jobs'::regclass`).Scan(&def).Error; err != nil {
		return err
	}
	if strings.Contains(def, "revert") {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE migration_jobs DROP CONSTRAINT IF EXISTS chk_migration_jobs_kind`,
			`ALTER TABLE migration_jobs ADD CONSTRAINT chk_migration_jobs_kind
				CHECK (kind IN ('up','rollback','forward','revert'))`,
		} {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func upgradeChangeRequestKind(db *gorm.DB) error {
	var def string
	if err := db.Raw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conname = 'chk_change_requests_kind' AND conrelid = 'change_requests'::regclass`).Scan(&def).Error; err != nil {
		return err
	}
	if strings.Contains(def, "revert") {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE change_requests DROP CONSTRAINT IF EXISTS chk_change_requests_kind`,
			`ALTER TABLE change_requests ADD CONSTRAINT chk_change_requests_kind
				CHECK (kind IN ('up','rollback','forward','revert'))`,
		} {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func upgradeMigrationStatus(db *gorm.DB) error {
	var def string
	err := db.Raw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conname = 'chk_migrations_status' AND conrelid = 'migrations'::regclass`).Scan(&def).Error
	if err != nil {
		return err
	}
	if strings.Contains(def, "pending_review") && strings.Contains(def, "partial") {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, stmt := range []string{
			`ALTER TABLE migrations ALTER COLUMN status TYPE varchar(20)`,
			`ALTER TABLE migrations DROP CONSTRAINT IF EXISTS chk_migrations_status`,
			`ALTER TABLE migrations ADD CONSTRAINT chk_migrations_status
				CHECK (status IN ('draft','pending_review','approved','success','failed','partial'))`,
		} {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
