package engine

import (
	"errors"
	"strings"
	"web_backend/internal/models"
)

var (
	ErrMigrationNotFound = errors.New("migration not found")
	ErrNotExecutable     = errors.New("migration cannot be executed in its current status")
	ErrJobActive         = errors.New("migration already has a queued or running job")
	ErrInvalidKind       = errors.New("unknown job kind")
	ErrIrreversible      = errors.New("migration is irreversible")
	ErrRollbackTarget    = errors.New("rollback_of must be a successful execution of this migration")
	ErrJobNotFound       = errors.New("job not found")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// ValidateDefinition memastikan migration punya SQL up, dan SQL down atau tanda irreversible beserta alasannya
func ValidateDefinition(m *models.Migration) error {

	switch {
	case strings.TrimSpace(m.SQL) == "":
		return &ValidationError{"sql", "required"}
	case m.Irreversible && strings.TrimSpace(m.IrreversibleReason) == "":
		return &ValidationError{"irreversible_reason", "required when the migration is irreversible"}
	case !m.Irreversible && strings.TrimSpace(m.SQLDown) == "":
		return &ValidationError{"sql_down", "required unless the migration is marked irreversible"}
	case m.BatchSize < 0:
		return &ValidationError{"batch_size", "must be >= 0"}
	case m.BatchSize > 1000000:
		return &ValidationError{"batch_size", "max 1000000"}
	}
	if m.BatchSize > 0 {
		upper := strings.ToUpper(strings.TrimSpace(m.SQL))
		switch {
		case m.Irreversible:
			return &ValidationError{"batch_size", "batch migration cannot be irreversible"}
		case !strings.Contains(m.SQL, ":last_id") || !strings.Contains(m.SQL, ":batch_size"):
			return &ValidationError{"sql", "batch SQL must contain :last_id and :batch_size"}
		case !strings.Contains(strings.ToUpper(m.SQL), "ORDER BY"):
			return &ValidationError{"sql", "batch SQL must use ORDER BY id"}
		case !strings.HasPrefix(upper, "UPDATE") && !strings.HasPrefix(upper, "DELETE") && !strings.HasPrefix(upper, "WITH"):
			return &ValidationError{"sql", "batch SQL must be UPDATE/DELETE/WITH"}
		case !strings.Contains(m.SQLDown, ":from_id") || !strings.Contains(m.SQLDown, ":to_id"):
			return &ValidationError{"sql_down", "batch SQL down must contain :from_id and :to_id"}
		}
	}
	return nil
}
