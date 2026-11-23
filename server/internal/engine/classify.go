package engine

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"web_backend/internal/targetdb"

	"github.com/jackc/pgx/v5/pgconn"
)

// classify menentukan apakah kegagalan bersifat sementara (boleh di-retry otomatis) dan mengambil SQLSTATE-nya.
func classify(err error) (sqlState string, transient bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := pgErr.Code
		switch {
		case strings.HasPrefix(code, "08"): // connection_exception
			return code, true
		case code == "40001", // serialization_failure
			code == "40P01", // deadlock_detected
			code == "55P03", // lock_not_available
			code == "53300", // too_many_connections
			code == "57P01", // admin_shutdown
			code == "57P02", // crash_shutdown
			code == "57P03": // cannot_connect_now
			return code, true
		}
		return code, false
	}
	
	switch {
	case errors.Is(err, targetdb.ErrDecrypt), errors.Is(err, targetdb.ErrNotFound),
		errors.Is(err, targetdb.ErrAuthFailed), errors.Is(err, targetdb.ErrDatabaseNotFound):
		return "", false
	case targetdb.IsConnError(err), pgconn.SafeToRetry(err), pgconn.Timeout(err),
		errors.Is(err, context.DeadlineExceeded):
		return "", true
	}

	return "", true
}

func backoff(attempt int, base, max time.Duration) time.Duration {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	return d
}

var passwordLiteral = regexp.MustCompile(`(?i)(password\s*(?:=\s*)?)'(?:[^']|'')*'`)

// redactSQL menyamarkan literal password sebelum SQL ditulis ke log.
func redactSQL(sql string) string {
	return passwordLiteral.ReplaceAllString(sql, "$1'***'")
}
