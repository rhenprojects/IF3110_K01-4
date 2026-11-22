package workflow

import (
	"errors"
	"reflect"
	"testing"
)

var order = []string{"A", "B", "C"}

func TestCheckForward(t *testing.T) {
	cases := []struct {
		name      string
		migration string
		order     []string
		history   []Execution
		wantErr   error
	}{
		{
			name:      "B di-rollback, A aktif: B boleh di-forward",
			migration: "B",
			order:     order,
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusSuccess),
				rollback("E3", "B", "E2", StatusSuccess),
			},
		},
		{
			name:      "C tidak boleh di-forward sebelum B aktif lagi",
			migration: "C",
			order:     order,
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusSuccess),
				up("E3", "C", StatusSuccess),
				rollback("E4", "C", "E3", StatusSuccess),
				rollback("E5", "B", "E2", StatusSuccess),
			},
			wantErr: ErrOutOfOrder,
		},
		{
			name:      "migration yang masih aktif tidak perlu di-forward",
			migration: "B",
			order:     order,
			history:   specHistory(),
			wantErr:   ErrAlreadyActive,
		},
		{
			name:      "migration yang belum pernah dijalankan adalah up, bukan forward",
			migration: "C",
			order:     order,
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusSuccess),
			},
			wantErr: ErrNeverExecuted,
		},
		{
			name:      "attempt gagal boleh di-forward lagi",
			migration: "B",
			order:     order,
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusFailed),
			},
		},
		{
			name:      "rollback gagal: migration tetap aktif",
			migration: "B",
			order:     order,
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusSuccess),
				rollback("E3", "B", "E2", StatusFailed),
			},
			wantErr: ErrAlreadyActive,
		},
		{
			name:      "migration pertama tidak punya prasyarat urutan",
			migration: "A",
			order:     order,
			history: []Execution{
				up("E1", "A", StatusSuccess),
				rollback("E2", "A", "E1", StatusSuccess),
			},
		},
		{
			name:      "migration di luar urutan",
			migration: "Z",
			order:     order,
			history:   specHistory(),
			wantErr:   ErrNotInSequence,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckForward(c.migration, c.order, c.history)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("tidak diharapkan error: %v", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("error = %v, mau %v", err, c.wantErr)
			}
		})
	}
}

// Rollback C, rollback B, lalu forward B dan C satu per satu.
func TestForward_Lifecycle(t *testing.T) {
	history := []Execution{
		up("E1", "A", StatusSuccess),
		up("E2", "B", StatusSuccess),
		up("E3", "C", StatusSuccess),
	}

	if _, err := RollbackTarget("C", true, history); err != nil {
		t.Fatalf("rollback C harus boleh: %v", err)
	}
	history = append(history, rollback("E4", "C", "E3", StatusSuccess))

	if _, err := RollbackTarget("B", true, history); err != nil {
		t.Fatalf("rollback B harus boleh setelah C: %v", err)
	}
	history = append(history, rollback("E5", "B", "E2", StatusSuccess))

	if err := CheckForward("C", order, history); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("forward C sebelum B harus ditolak, dapat: %v", err)
	}
	if err := CheckForward("B", order, history); err != nil {
		t.Fatalf("forward B harus boleh: %v", err)
	}
	history = append(history, forward("E6", "B", StatusSuccess))

	// Forward B tidak otomatis menjalankan C.
	if got, want := ids(ActiveExecutions(history)), []string{"E1", "E6"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("aktif = %v, mau %v", got, want)
	}
	if err := CheckForward("C", order, history); err != nil {
		t.Fatalf("forward C harus boleh setelah B aktif: %v", err)
	}
}