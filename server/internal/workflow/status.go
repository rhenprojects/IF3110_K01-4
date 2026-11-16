package workflow

import (
	"errors"
	"fmt"
)

// Alur: Draft → Pending Review → Approved → Pending (Queue) → Running → Success/Failed.
type Status string

const (
	StatusDraft         Status = "draft"
	StatusPendingReview Status = "pending_review"
	StatusApproved      Status = "approved"
	StatusQueued        Status = "queued"
	StatusRunning       Status = "running"
	StatusSuccess       Status = "success"
	StatusFailed        Status = "failed"
)

var AllStatuses = []Status{
	StatusDraft,
	StatusPendingReview,
	StatusApproved,
	StatusQueued,
	StatusRunning,
	StatusSuccess,
	StatusFailed,
}

var (
	ErrUnknownStatus     = errors.New("status tidak dikenal")
	ErrInvalidTransition = errors.New("perpindahan status tidak diizinkan")
)

// Success dan Failed bersifat final: tidak punya perpindahan keluar.
var allowed = map[Status][]Status{
	StatusDraft:         {StatusPendingReview},
	StatusPendingReview: {StatusApproved, StatusDraft}, // Draft = ditolak
	StatusApproved:      {StatusQueued},
	StatusQueued:        {StatusRunning},
	StatusRunning:       {StatusSuccess, StatusFailed},
}

func IsValid(s Status) bool {
	for _, known := range AllStatuses {
		if s == known {
			return true
		}
	}
	return false
}

func IsFinal(s Status) bool {
	return IsValid(s) && len(allowed[s]) == 0
}

func CanTransition(from, to Status) bool {
	for _, next := range allowed[from] {
		if next == to {
			return true
		}
	}
	return false
}

func Transition(from, to Status) (Status, error) {
	if !IsValid(from) {
		return from, fmt.Errorf("%w: asal %q", ErrUnknownStatus, from)
	}
	if !IsValid(to) {
		return from, fmt.Errorf("%w: tujuan %q", ErrUnknownStatus, to)
	}
	if !CanTransition(from, to) {
		return from, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
	}
	return to, nil
}
