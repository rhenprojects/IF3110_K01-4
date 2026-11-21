package models

// All mengembalikan seluruh model database platform dalam urutan dependensi (induk sebelum anak).
func All() []any {
	return []any{
		&User{},
		&Group{},
		&GroupMember{},
		&Session{},
		&DBConnection{},
		&GroupDatabaseAccess{},
		&Migration{},
		&MigrationBatch{},
		&MigrationBatchItem{},
		&MigrationJob{},
		&MigrationExecution{},
	}
}
