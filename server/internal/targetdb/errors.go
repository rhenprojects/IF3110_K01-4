package targetdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound         = errors.New("database connection not found")
	ErrDuplicateName    = errors.New("database connection name already exists")
	ErrAuthFailed       = errors.New("authentication to target database failed")
	ErrDatabaseNotFound = errors.New("target database does not exist")
	ErrTimeout          = errors.New("timed out connecting to target database")
	ErrUnreachable      = errors.New("cannot connect to target database")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func classifyConnError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000":
			return fmt.Errorf("%w: %w", ErrAuthFailed, err)
		case "3D000":
			return fmt.Errorf("%w: %w", ErrDatabaseNotFound, err)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || pgconn.Timeout(err) {
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	}
	return fmt.Errorf("%w: %w", ErrUnreachable, err)
}

func ClassifyConnError(err error) error {
	return classifyConnError(err)
}

func IsConnError(err error) bool {
	return errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrDatabaseNotFound) ||
		errors.Is(err, ErrTimeout) || errors.Is(err, ErrUnreachable)
}
