package workflow

import (
	"errors"
	"fmt"
)

var (
	ErrAlreadyActive = errors.New("migration masih aktif")
	ErrNeverExecuted = errors.New("migration belum pernah dieksekusi")
	ErrOutOfOrder    = errors.New("forward harus mengikuti urutan linear")
	ErrNotInSequence = errors.New("migration tidak ada dalam urutan")
)

// Menjalankan ulang migration yang tidak aktif (di-rollback atau gagal) sesuai urutan.
func CheckForward(migrationID string, order []string, history []Execution) error {
	idx := -1
	for i, id := range order {
		if id == migrationID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrNotInSequence
	}

	active := make(map[string]bool)
	for _, e := range ActiveExecutions(history) {
		active[e.MigrationID] = true
	}
	if active[migrationID] {
		return ErrAlreadyActive
	}

	attempted := false
	for _, e := range history {
		if e.MigrationID == migrationID {
			attempted = true
			break
		}
	}
	if !attempted {
		return ErrNeverExecuted
	}

	for _, prev := range order[:idx] {
		if !active[prev] {
			return fmt.Errorf("%w: %s harus aktif lebih dulu", ErrOutOfOrder, prev)
		}
	}
	return nil
}
