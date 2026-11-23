package workflow

import (
	"errors"
	"testing"
)

// Seluruh perpindahan valid
var validPairs = map[[2]Status]bool{
	{StatusDraft, StatusPendingReview}:    true,
	{StatusPendingReview, StatusApproved}: true,
	{StatusPendingReview, StatusDraft}:    true,
	{StatusApproved, StatusQueued}:        true,
	{StatusQueued, StatusRunning}:         true,
	{StatusRunning, StatusSuccess}:        true,
	{StatusRunning, StatusFailed}:         true,
}

func TestTransition_AllPairs(t *testing.T) {
	for _, from := range AllStatuses {
		for _, to := range AllStatuses {
			want := validPairs[[2]Status{from, to}]

			got, err := Transition(from, to)

			if want {
				if err != nil {
					t.Errorf("%s → %s seharusnya diizinkan, tapi error: %v", from, to, err)
				}
				if got != to {
					t.Errorf("%s → %s: status hasil = %s, mau %s", from, to, got, to)
				}
				continue
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("%s → %s seharusnya ditolak dengan ErrInvalidTransition, dapat: %v", from, to, err)
			}
			if got != from {
				t.Errorf("%s → %s ditolak, status harus tetap %s, dapat %s", from, to, from, got)
			}
		}
	}
}

// Draft hanya boleh ke Pending Review, review tidak boleh dilewati.
func TestTransition_CannotSkipReview(t *testing.T) {
	for _, to := range []Status{StatusApproved, StatusQueued, StatusRunning, StatusSuccess} {
		if _, err := Transition(StatusDraft, to); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("Draft → %s harus ditolak, dapat: %v", to, err)
		}
	}
}

func TestTransition_RejectedReturnsToDraft(t *testing.T) {
	got, err := Transition(StatusPendingReview, StatusDraft)
	if err != nil || got != StatusDraft {
		t.Fatalf("ditolak harus bisa kembali ke Draft, dapat %s, err %v", got, err)
	}
}

func TestTransition_UnknownStatus(t *testing.T) {
	cases := []struct{ from, to Status }{
		{"ngawur", StatusDraft},
		{StatusDraft, "ngawur"},
		{"", ""},
	}
	for _, c := range cases {
		if _, err := Transition(c.from, c.to); !errors.Is(err, ErrUnknownStatus) {
			t.Errorf("Transition(%q, %q) harus ErrUnknownStatus, dapat: %v", c.from, c.to, err)
		}
	}
}

func TestIsFinal(t *testing.T) {
	final := map[Status]bool{StatusSuccess: true, StatusFailed: true}
	for _, s := range AllStatuses {
		if IsFinal(s) != final[s] {
			t.Errorf("IsFinal(%s) = %v, mau %v", s, IsFinal(s), final[s])
		}
	}
	if IsFinal("ngawur") {
		t.Error("status tidak dikenal bukan status final")
	}
}
