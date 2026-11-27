package workflow

import (
	"errors"
	"reflect"
	"testing"
)

func up(id, mig string, st Status) Execution {
	return Execution{ID: id, MigrationID: mig, Kind: KindUp, Status: st}
}

func forward(id, mig string, st Status) Execution {
	return Execution{ID: id, MigrationID: mig, Kind: KindForward, Status: st}
}

func rollback(id, mig, of string, st Status) Execution {
	return Execution{ID: id, MigrationID: mig, Kind: KindRollback, Status: st, RollbackOf: of}
}

func ids(list []Execution) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.ID)
	}
	return out
}

// Contoh E1–E5 dari spec: A, B, rollback B, forward B, C.
func specHistory() []Execution {
	return []Execution{
		up("E1", "A", StatusSuccess),
		up("E2", "B", StatusSuccess),
		rollback("E3", "B", "E2", StatusSuccess),
		forward("E4", "B", StatusSuccess),
		up("E5", "C", StatusSuccess),
	}
}

func TestHideRollback_SpecExample(t *testing.T) {
	history := specHistory()

	got := ids(HideRollback(history))

	if want := []string{"E1", "E4", "E5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Hide rollback = %v, mau %v", got, want)
	}
	if len(history) != 5 {
		t.Fatalf("riwayat asli tidak boleh berubah, panjang = %d", len(history))
	}
}

func TestHideRollback_FailedRollbackStaysVisible(t *testing.T) {
	history := []Execution{
		up("E1", "A", StatusSuccess),
		up("E2", "B", StatusSuccess),
		rollback("E3", "B", "E2", StatusFailed),
	}
	got := ids(HideRollback(history))
	if want := []string{"E1", "E2", "E3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rollback gagal tidak boleh menyembunyikan apa pun: %v, mau %v", got, want)
	}
}

func TestHideRollback_FailedAttemptStaysVisible(t *testing.T) {
	history := []Execution{
		up("E1", "A", StatusSuccess),
		up("E2", "B", StatusFailed),
	}
	got := ids(HideRollback(history))
	if want := []string{"E1", "E2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attempt gagal harus tetap terlihat: %v, mau %v", got, want)
	}
}

func TestHideRollback_MultiplePairs(t *testing.T) {
	history := []Execution{
		up("E1", "A", StatusSuccess),
		up("E2", "B", StatusSuccess),
		up("E3", "C", StatusSuccess),
		rollback("E4", "C", "E3", StatusSuccess),
		rollback("E5", "B", "E2", StatusSuccess),
	}
	got := ids(HideRollback(history))
	if want := []string{"E1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, mau %v", got, want)
	}
}

func TestHideRollback_EmptyHistory(t *testing.T) {
	if got := HideRollback(nil); len(got) != 0 {
		t.Fatalf("riwayat kosong harus menghasilkan kosong, dapat %v", got)
	}
}

func TestActiveExecutions_SpecExample(t *testing.T) {
	got := ids(ActiveExecutions(specHistory()))
	if want := []string{"E1", "E4", "E5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("aktif = %v, mau %v", got, want)
	}
}

func TestActiveExecutions_FailedDoesNotChangePosition(t *testing.T) {
	history := []Execution{
		up("E1", "A", StatusSuccess),
		up("E2", "B", StatusSuccess),
		up("E3", "C", StatusFailed),
		rollback("E4", "B", "E2", StatusFailed),
	}
	got := ids(ActiveExecutions(history))
	if want := []string{"E1", "E2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("aktif = %v, mau %v", got, want)
	}
}

func TestRollbackTarget(t *testing.T) {
	cases := []struct {
		name       string
		history    []Execution
		migration  string
		reversible bool
		wantID     string
		wantErr    error
	}{
		{
			name:       "migration aktif terakhir boleh di-rollback",
			history:    specHistory(),
			migration:  "C",
			reversible: true,
			wantID:     "E5",
		},
		{
			name:       "B ditahan selama C masih aktif",
			history:    specHistory(),
			migration:  "B",
			reversible: true,
			wantErr:    ErrNotLastActive,
		},
		{
			name:       "A ditahan selama B dan C masih aktif",
			history:    specHistory(),
			migration:  "A",
			reversible: true,
			wantErr:    ErrNotLastActive,
		},
		{
			name:       "irreversible ditolak",
			history:    specHistory(),
			migration:  "C",
			reversible: false,
			wantErr:    ErrIrreversible,
		},
		{
			name:       "migration yang belum pernah dijalankan",
			history:    specHistory(),
			migration:  "Z",
			reversible: true,
			wantErr:    ErrNotActive,
		},
		{
			name: "migration yang sudah di-rollback tidak bisa di-rollback lagi",
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusSuccess),
				rollback("E3", "B", "E2", StatusSuccess),
			},
			migration:  "B",
			reversible: true,
			wantErr:    ErrNotActive,
		},
		{
			name: "setelah C di-rollback, giliran B",
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusSuccess),
				up("E3", "C", StatusSuccess),
				rollback("E4", "C", "E3", StatusSuccess),
			},
			migration:  "B",
			reversible: true,
			wantID:     "E2",
		},
		{
			name: "rollback gagal tidak mengubah posisi aktif",
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusSuccess),
				rollback("E3", "B", "E2", StatusFailed),
			},
			migration:  "B",
			reversible: true,
			wantID:     "E2",
		},
		{
			name: "eksekusi gagal bukan migration aktif",
			history: []Execution{
				up("E1", "A", StatusSuccess),
				up("E2", "B", StatusFailed),
			},
			migration:  "B",
			reversible: true,
			wantErr:    ErrNotActive,
		},
		{
			name:       "riwayat kosong",
			migration:  "A",
			reversible: true,
			wantErr:    ErrNotActive,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RollbackTarget(c.migration, c.reversible, c.history)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("error = %v, mau %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("tidak diharapkan error: %v", err)
			}
			if got.ID != c.wantID {
				t.Errorf("target = %s, mau %s", got.ID, c.wantID)
			}
		})
	}
}

// Batch yang di-revert penuh tidak lagi menahan rollback migration sebelumnya.
func TestRollbackTargetExcludingReverted(t *testing.T) {
	history := []Execution{
		{ID: "E1", MigrationID: "A", Kind: KindUp, Status: StatusSuccess},
		{ID: "E2", MigrationID: "B", Kind: KindUp, Status: StatusSuccess},
	}
	if _, err := RollbackTarget("A", true, history); !errors.Is(err, ErrNotLastActive) {
		t.Fatalf("tanpa pengecualian: error = %v, mau ErrNotLastActive", err)
	}
	got, err := RollbackTargetExcluding("A", true, history, map[string]bool{"B": true})
	if err != nil || got.ID != "E1" {
		t.Fatalf("dengan pengecualian: got %+v err %v, mau E1", got, err)
	}
}
