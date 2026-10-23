package targetdb

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func validInput() ConnectionInput {
	return ConnectionInput{
		Name: "  analytics  ", Host: "localhost", DBName: "analytics",
		Username: "app", Password: "secret",
	}
}

func TestNormalizeAppliesDefaults(t *testing.T) {
	in := validInput()
	if err := in.normalize(true); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if in.Name != "analytics" {
		t.Errorf("name = %q, want trimmed", in.Name)
	}
	if in.Port != 5432 {
		t.Errorf("port = %d, want 5432", in.Port)
	}
	if in.SSLMode != "prefer" {
		t.Errorf("ssl_mode = %q, want prefer", in.SSLMode)
	}
}

func TestNormalizeRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		field  string
		mutate func(*ConnectionInput)
	}{
		{"name", func(in *ConnectionInput) { in.Name = " " }},
		{"host", func(in *ConnectionInput) { in.Host = "" }},
		{"host", func(in *ConnectionInput) { in.Host = "a.example.com,b.example.com" }},
		{"host", func(in *ConnectionInput) { in.Host = "/var/run/postgresql" }},
		{"port", func(in *ConnectionInput) { in.Port = 70000 }},
		{"db_name", func(in *ConnectionInput) { in.DBName = "" }},
		{"username", func(in *ConnectionInput) { in.Username = "" }},
		{"password", func(in *ConnectionInput) { in.Password = "" }},
		{"ssl_mode", func(in *ConnectionInput) { in.SSLMode = "sometimes" }},
	}
	for _, tc := range cases {
		in := validInput()
		tc.mutate(&in)
		err := in.normalize(true)
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Field != tc.field {
			t.Errorf("%+v: err = %v, want ValidationError on %q", in, err, tc.field)
		}
	}
}

func TestNormalizeAllowsEmptyPasswordOnUpdate(t *testing.T) {
	in := validInput()
	in.Password = ""
	if err := in.normalize(false); err != nil {
		t.Fatalf("normalize(update): %v", err)
	}
}

func TestClassifyConnError(t *testing.T) {
	cases := []struct {
		err  error
		want error
	}{
		{&pgconn.PgError{Code: "28P01"}, ErrAuthFailed},
		{&pgconn.PgError{Code: "28000"}, ErrAuthFailed},
		{&pgconn.PgError{Code: "3D000"}, ErrDatabaseNotFound},
		{fmt.Errorf("connect: %w", context.DeadlineExceeded), ErrTimeout},
		{errors.New("dial tcp: connection refused"), ErrUnreachable},
	}
	for _, tc := range cases {
		got := classifyConnError(tc.err)
		if !errors.Is(got, tc.want) {
			t.Errorf("classifyConnError(%v) = %v, want %v", tc.err, got, tc.want)
		}
		if !errors.Is(got, tc.err) {
			t.Errorf("classifyConnError(%v) lost the original error", tc.err)
		}
		if !IsConnError(got) {
			t.Errorf("IsConnError(%v) = false", got)
		}
	}
}

func TestTranslateDBError(t *testing.T) {
	if err := translateDBError(&pgconn.PgError{Code: "23505"}); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("unique violation = %v, want ErrDuplicateName", err)
	}
	other := errors.New("boom")
	if err := translateDBError(other); err != other {
		t.Errorf("other error = %v, want unchanged", err)
	}
}

func TestServiceRejectsMalformedIDWithoutQuerying(t *testing.T) {
	svc := NewService(nil, nil, NewPoolManager())
	if _, err := svc.Get(context.Background(), "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get err = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(context.Background(), "1; DROP TABLE x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete err = %v, want ErrNotFound", err)
	}
}
