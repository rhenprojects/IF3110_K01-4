package models

import "gorm.io/gorm"

// Migrate membuat/menyesuaikan seluruh tabel platform, lalu memasang trigger yang membuat migration_executions append-only
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(All()...); err != nil {
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
