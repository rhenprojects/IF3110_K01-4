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

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message}

// ValidateDefinition memastikan migration punya SQL up, dan SQL down atau tanda irreversible beserta alasannya
func ValidateDefinition(m *models.Migration) error {
	
	switch {
	case strings.TrimSpace(m.SQL) == "":
		return &ValidationError{"sql", "required"}
	case m.Irreversible && strings.TrimSpace(m.IrreversibleReason) == "":
		return &ValidationError{"irreversible_reason", "required when the migration is irreversible"}
	case !m.Irreversible && strings.TrimSpace(m.SQLDown) == "":
		return &ValidationError{"sql_down", "required unless the migration is marked irreversible"}
	}

	return nil
}
