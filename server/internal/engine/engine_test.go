package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"web_backend/internal/models"
	"web_backend/internal/targetdb"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		sqlState  string
		transient bool
	}{
		{"syntax error", &pgconn.PgError{Code: "42601"}, "42601", false},
		{"undefined table", &pgconn.PgError{Code: "42P01"}, "42P01", false},
		{"unique violation", &pgconn.PgError{Code: "23505"}, "23505", false},
		{"serialization failure", &pgconn.PgError{Code: "40001"}, "40001", true},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, "40P01", true},
		{"lock not available", &pgconn.PgError{Code: "55P03"}, "55P03", true},
		{"connection failure", &pgconn.PgError{Code: "08006"}, "08006", true},
		{"admin shutdown", &pgconn.PgError{Code: "57P01"}, "57P01", true},
		{"unreachable target", fmt.Errorf("%w: dial", targetdb.ErrUnreachable), "", true},
		{"deadline", context.DeadlineExceeded, "", true},
		{"wrong password", fmt.Errorf("%w: x", targetdb.ErrAuthFailed), "", false},
		{"cannot decrypt", targetdb.ErrDecrypt, "", false},
		{"connection deleted", targetdb.ErrNotFound, "", false},
		{"broken pipe", errors.New("write: broken pipe"), "", true},
	}
	for _, tc := range cases {
		state, transient := classify(tc.err)
		if state != tc.sqlState || transient != tc.transient {
			t.Errorf("%s: classify = (%q, %v), want (%q, %v)", tc.name, state, transient, tc.sqlState, tc.transient)
		}
	}
}

func TestBackoffDoublesUpToMax(t *testing.T) {
	base, max := 2*time.Second, 10*time.Second
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, w := range want {
		if got := backoff(i+1, base, max); got != w {
			t.Errorf("backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestRedactSQL(t *testing.T) {
	cases := map[string]string{
		`ALTER ROLE app PASSWORD 'hunter2'`:                `ALTER ROLE app PASSWORD '***'`,
		`CREATE USER x WITH password='it''s secret' LOGIN`: `CREATE USER x WITH password='***' LOGIN`,
		`UPDATE t SET note = 'password reset'`:             `UPDATE t SET note = 'password reset'`,
		`CREATE TABLE t (id int)`:                          `CREATE TABLE t (id int)`,
	}
	for in, want := range cases {
		if got := redactSQL(in); got != want {
			t.Errorf("redactSQL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateDefinition(t *testing.T) {
	cases := []struct {
		m     models.Migration
		field string
	}{
		{models.Migration{SQL: "CREATE TABLE t (id int)", SQLDown: "DROP TABLE t"}, ""},
		{models.Migration{SQL: "DELETE FROM t", Irreversible: true, IrreversibleReason: "data loss"}, ""},
		{models.Migration{SQL: "  ", SQLDown: "x"}, "sql"},
		{models.Migration{SQL: "CREATE TABLE t (id int)"}, "sql_down"},
		{models.Migration{SQL: "DELETE FROM t", Irreversible: true}, "irreversible_reason"},
	}
	for _, tc := range cases {
		err := ValidateDefinition(&tc.m)
		var verr *ValidationError
		switch {
		case tc.field == "" && err != nil:
			t.Errorf("%+v: unexpected %v", tc.m, err)
		case tc.field != "" && (!errors.As(err, &verr) || verr.Field != tc.field):
			t.Errorf("%+v: err %v, want field %q", tc.m, err, tc.field)
		}
	}
}

func TestSQLForUsesOriginalUpAndDown(t *testing.T) {
	m := &models.Migration{SQL: "UP", SQLDown: "DOWN"}
	if sqlFor(m, models.JobKindUp) != "UP" || sqlFor(m, models.JobKindForward) != "UP" || sqlFor(m, models.JobKindRollback) != "DOWN" {
		t.Error("sqlFor picked the wrong SQL")
	}
}
