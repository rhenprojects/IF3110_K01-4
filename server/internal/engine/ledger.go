package engine

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ledger adalah bukti eksekusi yang disimpan di database target
const (
	ledgerCreateSchema = `CREATE SCHEMA IF NOT EXISTS _dbmp`
	ledgerCreateTable  = `CREATE TABLE IF NOT EXISTS _dbmp.executions (
	execution_id uuid PRIMARY KEY,
	migration_id uuid NOT NULL,
	kind text NOT NULL,
	committed_at timestamptz NOT NULL DEFAULT now()
)`
)

func ensureLedger(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range []string{ledgerCreateSchema, ledgerCreateTable} {
		if _, err := pool.Exec(ctx, stmt); err != nil && !isDuplicateObject(err) {
			return err
		}
	}
	return nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func ledgerHas(ctx context.Context, q querier, executionID string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM _dbmp.executions WHERE execution_id = $1)`, executionID).Scan(&exists)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "42P01" || pgErr.Code == "3F000") {
		return false, nil
	}
	return exists, err
}

func ledgerRecord(ctx context.Context, tx pgx.Tx, executionID, migrationID, kind string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO _dbmp.executions (execution_id, migration_id, kind) VALUES ($1, $2, $3)`,
		executionID, migrationID, kind)
	return err
}

func isDuplicateObject(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "42P06" || pgErr.Code == "42P07")
}
