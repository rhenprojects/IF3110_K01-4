package workflow

import (
	"errors"
	"fmt"
)

type Kind string

const (
	KindUp       Kind = "up"
	KindRollback Kind = "rollback"
	KindForward  Kind = "forward"
)

// Urutan slice = urutan kronologis. RollbackOf hanya diisi untuk Kind rollback.
type Execution struct {
	ID          string
	MigrationID string
	Kind        Kind
	Status      Status
	RollbackOf  string
}

var (
	ErrNotActive     = errors.New("migration tidak aktif")
	ErrIrreversible  = errors.New("migration irreversible")
	ErrNotLastActive = errors.New("hanya migration aktif terakhir yang boleh di-rollback")
)

// Aktif = up/forward sukses yang belum dibatalkan oleh rollback sukses.
func ActiveExecutions(history []Execution) []Execution {
	cancelled := make(map[string]bool)
	for _, e := range history {
		if e.Kind == KindRollback && e.Status == StatusSuccess {
			cancelled[e.RollbackOf] = true
		}
	}

	var active []Execution
	for _, e := range history {
		isApply := e.Kind == KindUp || e.Kind == KindForward
		if isApply && e.Status == StatusSuccess && !cancelled[e.ID] {
			active = append(active, e)
		}
	}
	return active
}

// Spec: rollback hanya untuk migration aktif terakhir yang reversible.
func RollbackTarget(migrationID string, reversible bool, history []Execution) (Execution, error) {
	active := ActiveExecutions(history)

	idx := -1
	for i, e := range active {
		if e.MigrationID == migrationID {
			idx = i
		}
	}
	if idx < 0 {
		return Execution{}, ErrNotActive
	}
	if !reversible {
		return Execution{}, ErrIrreversible
	}
	if last := active[len(active)-1]; idx != len(active)-1 {
		return Execution{}, fmt.Errorf("%w: rollback %s lebih dulu", ErrNotLastActive, last.MigrationID)
	}
	return active[idx], nil
}

// Spec: sembunyikan eksekusi yang dibatalkan dan rollback sukses yang membatalkannya, dipasangkan lewat execution ID.
func HideRollback(history []Execution) []Execution {
	hidden := make(map[string]bool)
	for _, e := range history {
		if e.Kind == KindRollback && e.Status == StatusSuccess && e.RollbackOf != "" {
			hidden[e.ID] = true
			hidden[e.RollbackOf] = true
		}
	}

	visible := make([]Execution, 0, len(history))
	for _, e := range history {
		if !hidden[e.ID] {
			visible = append(visible, e)
		}
	}
	return visible
}
