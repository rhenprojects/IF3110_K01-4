package grouphandler

import (
	"errors"
	"fmt"
	"strings"
	"web_backend/internal/models"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrGroupNotFound = errors.New("group not found")
	ErrDuplicateName = errors.New("group name already exists")
	ErrMemberExists  = errors.New("user is already a member of this group")
	ErrMemberNotFind = errors.New("group member not found")
	ErrAccessNotFind = errors.New("group database access not found")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", &ValidationError{"name", "required"}
	case len(name) > 100:
		return "", &ValidationError{"name", "max 100 characters"}
	}
	return name, nil
}

func validateDescription(description string) (string, error) {
	description = strings.TrimSpace(description)
	if len(description) > 255 {
		return "", &ValidationError{"description", "max 255 characters"}
	}
	return description, nil
}

func validateLevel(level string) (models.AccessLevel, error) {
	switch models.AccessLevel(strings.TrimSpace(level)) {
	case models.AccessView:
		return models.AccessView, nil
	case models.AccessEdit:
		return models.AccessEdit, nil
	}
	return models.AccessNone, &ValidationError{"level", "must be one of view, edit"}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func translateDBError(err error) error {
	if isUniqueViolation(err) {
		return ErrDuplicateName
	}
	return err
}
