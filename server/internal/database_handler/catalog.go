package databasehandler

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"web_backend/internal/models"
	"web_backend/internal/targetdb"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrTableNotFound    = errors.New("table not found")
	ErrTargetPermission = errors.New("target database denied access to this object")
	ErrQueryTimeout     = errors.New("query on target database timed out")
	ErrQueryFailed      = errors.New("query on target database failed")
)

const viewerQueryTimeout = 10 * time.Second

// querier dipenuhi oleh pgx.Tx maupun *pgxpool.Pool.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var viewerTracer = otel.Tracer("web_backend/internal/database_handler")

// viewerRead mengidentifikasi siapa membaca apa, untuk log dan span query viewer.
type viewerRead struct {
	actor     string
	database  string
	operation string// schema atau rows
}

func readOnly(ctx context.Context, pool *pgxpool.Pool, read viewerRead, fn func(q querier) error) (err error) {
	ctx, span := viewerTracer.Start(ctx, "viewer.read", trace.WithAttributes(
		attribute.String("db.connection_id", read.database), attribute.String("viewer.operation", read.operation)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return translateTargetError(err)
	}
	defer tx.Rollback(context.Background())

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", viewerQueryTimeout.Milliseconds())); err != nil {
		return translateTargetError(err)
	}
	return translateTargetError(fn(&loggedQuerier{q: tx, parent: ctx, read: read}))
}

// loggedQuerier mencatat setiap query read-only viewer sebagai log JSON (event sql.query) dan span db.query.
type loggedQuerier struct {
	q      querier
	parent context.Context // context span viewer.read
	read   viewerRead
}

func (l *loggedQuerier) start(sql string) (context.Context, trace.Span, time.Time) {
	ctx, span := viewerTracer.Start(l.parent, "db.query", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system.name", "postgresql"), attribute.String("db.query.text", compactSQL(sql))))
	return ctx, span, time.Now()
}

func (l *loggedQuerier) finish(ctx context.Context, span trace.Span, started time.Time, sql string, args []any, rows int64, err error) {
	duration := time.Since(started)
	span.SetAttributes(attribute.Int64("db.response.returned_rows", rows))
	attrs := []any{"event", "sql.query", "actor", l.read.actor, "database", l.read.database,
		"operation", l.read.operation, "query", compactSQL(sql), "params", args, "rows", rows,
		"duration_ms", duration.Milliseconds()}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			attrs = append(attrs, "sqlstate", pgErr.Code)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		slog.WarnContext(ctx, "viewer query failed", append(attrs, "status", "failed", "error", err.Error())...)
		return
	}
	span.End()
	slog.InfoContext(ctx, "viewer query executed", append(attrs, "status", "success")...)
}

func (l *loggedQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	spanCtx, span, started := l.start(sql)
	rows, err := l.q.Query(ctx, sql, args...)
	if err != nil {
		l.finish(spanCtx, span, started, sql, args, 0, err)
		return nil, err
	}
	return &loggedRows{Rows: rows, done: func(n int64, err error) { l.finish(spanCtx, span, started, sql, args, n, err) }}, nil
}

func (l *loggedQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	spanCtx, span, started := l.start(sql)
	return loggedRow{row: l.q.QueryRow(ctx, sql, args...), done: func(err error) {
		rows := int64(1)
		if errors.Is(err, pgx.ErrNoRows) {
			rows, err = 0, nil
		} else if err != nil {
			rows = 0
		}
		l.finish(spanCtx, span, started, sql, args, rows, err)
	}}
}

// loggedRows menghitung row yang dibaca dan mencatat query saat hasilnya selesai dibaca atau ditutup.
type loggedRows struct {
	pgx.Rows
	n        int64
	finished bool
	done     func(rows int64, err error)
}

func (r *loggedRows) Next() bool {
	if r.Rows.Next() {
		r.n++
		return true
	}
	r.finish()
	return false
}

func (r *loggedRows) Close() {
	r.Rows.Close()
	r.finish()
}

func (r *loggedRows) finish() {
	if !r.finished {
		r.finished = true
		r.done(r.n, r.Rows.Err())
	}
}

type loggedRow struct {
	row  pgx.Row
	done func(err error)
}

func (r loggedRow) Scan(dest ...any) error {
	err := r.row.Scan(dest...)
	r.done(err)
	return err
}

// compactSQL merapikan whitespace query katalog yang ditulis multi-baris agar log tetap satu baris.
func compactSQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

func translateTargetError(err error) error {
	if err == nil || errors.Is(err, ErrTableNotFound) || targetdb.IsConnError(err) {
		return err
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return targetdb.ClassifyConnError(err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42501": // insufficient_privilege
			return fmt.Errorf("%w: %w", ErrTargetPermission, err)
		case "57014": // query_canceled, termasuk statement_timeout
			return fmt.Errorf("%w: %w", ErrQueryTimeout, err)
		}
		return fmt.Errorf("%w: %w", ErrQueryFailed, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrQueryTimeout, err)
	}
	if pgconn.SafeToRetry(err) {
		return targetdb.ClassifyConnError(err)
	}
	return err
}

func collectStrings(ctx context.Context, q querier, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func readSchema(ctx context.Context, q querier) ([]models.SchemaInfo, error) {
	// '\_' karena '_' adalah wildcard LIKE; tanpa escape schema seperti "pgbench" ikut tersaring.
	names, err := collectStrings(ctx, q, `
		SELECT n.nspname::text
		FROM pg_namespace n
		WHERE n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname`)
	if err != nil {
		return nil, err
	}

	schemas := make([]models.SchemaInfo, 0, len(names))
	for _, name := range names {
		tables, err := readTables(ctx, q, name)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, models.SchemaInfo{Name: name, Tables: tables})
	}
	return schemas, nil
}

func readTables(ctx context.Context, q querier, schema string) ([]models.SchemaTable, error) {
	type relation struct {
		Name string
		Kind string
	}
	rows, err := q.Query(ctx, `
		SELECT c.relname::text, c.relkind::text
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm')
		ORDER BY c.relname`, schema)
	if err != nil {
		return nil, err
	}
	relations, err := pgx.CollectRows(rows, pgx.RowToStructByPos[relation])
	if err != nil {
		return nil, err
	}

	columns, err := readColumnsBySchema(ctx, q, schema)
	if err != nil {
		return nil, err
	}
	constraints, err := readConstraintsBySchema(ctx, q, schema)
	if err != nil {
		return nil, err
	}
	indexes, err := readIndexesBySchema(ctx, q, schema)
	if err != nil {
		return nil, err
	}
	tables := make([]models.SchemaTable, 0, len(relations))
	for _, rel := range relations {
		tableType := "table"
		if rel.Kind == "v" || rel.Kind == "m" {
			tableType = "view"
		}
		// Tabel tanpa constraint/index (mis. hasil CREATE TABLE AS) tidak punya
		// entri di map; kirim array kosong supaya klien tidak menerima null.
		cols, cons, idxs := columns[rel.Name], constraints[rel.Name], indexes[rel.Name]
		if cols == nil {
			cols = []models.SchemaColumn{}
		}
		if cons == nil {
			cons = []models.SchemaConstraint{}
		}
		if idxs == nil {
			idxs = []models.SchemaIndex{}
		}
		tables = append(tables, models.SchemaTable{
			Name: rel.Name, Type: tableType,
			Columns: cols, Constraints: cons, Indexes: idxs,
		})
	}
	return tables, nil
}

func readColumns(ctx context.Context, q querier, schema, table string) ([]models.SchemaColumn, error) {
	rows, err := q.Query(ctx, `
		SELECT a.attname::text,
		       pg_catalog.format_type(a.atttypid, a.atttypmod),
		       NOT a.attnotnull,
		       pg_get_expr(d.adbin, d.adrelid),
		       EXISTS (
		           SELECT 1 FROM pg_constraint p
		           WHERE p.conrelid = c.oid AND p.contype = 'p' AND a.attnum = ANY(p.conkey)
		       )
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make([]models.SchemaColumn, 0)
	for rows.Next() {
		var col models.SchemaColumn
		var def pgtype.Text
		if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &def, &col.IsPrimaryKey); err != nil {
			return nil, err
		}
		if def.Valid {
			value := def.String
			col.Default = &value
		}
		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func readConstraints(ctx context.Context, q querier, schema, table string) ([]models.SchemaConstraint, error) {
	rows, err := q.Query(ctx, `
		SELECT con.conname::text, con.contype::text, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND con.contype IN ('p', 'f', 'u', 'c')
		ORDER BY con.conname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	constraints := make([]models.SchemaConstraint, 0)
	for rows.Next() {
		var item models.SchemaConstraint
		var kind string
		if err := rows.Scan(&item.Name, &kind, &item.Definition); err != nil {
			return nil, err
		}
		item.Type = constraintType(kind)
		constraints = append(constraints, item)
	}
	return constraints, rows.Err()
}

func constraintType(kind string) string {
	switch kind {
	case "p":
		return "primary_key"
	case "f":
		return "foreign_key"
	case "u":
		return "unique"
	case "c":
		return "check"
	}
	return kind
}

func readIndexes(ctx context.Context, q querier, schema, table string) ([]models.SchemaIndex, error) {
	rows, err := q.Query(ctx, `
		SELECT i.relname::text, ix.indisunique, pg_get_indexdef(ix.indexrelid)
		FROM pg_index ix
		JOIN pg_class t ON t.oid = ix.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_class i ON i.oid = ix.indexrelid
		WHERE n.nspname = $1 AND t.relname = $2
		ORDER BY i.relname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	indexes := make([]models.SchemaIndex, 0)
	for rows.Next() {
		var item models.SchemaIndex
		if err := rows.Scan(&item.Name, &item.IsUnique, &item.Definition); err != nil {
			return nil, err
		}
		indexes = append(indexes, item)
	}
	return indexes, rows.Err()
}

func readRows(ctx context.Context, q querier, schema, table string, limit, offset int) (*models.RowsResponse, error) {
	columns, err := readTableColumns(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}
	orderBy, err := primaryKeyColumns(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}

	total, estimated, err := estimatedRows(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}
	if !estimated {
		if err := q.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{schema, table}.Sanitize()).Scan(&total); err != nil {
			return nil, err
		}
	}

	names := make([]string, len(columns))
	for i, col := range columns {
		names[i] = col.Name
	}
	rows, err := q.Query(ctx, buildRowsQuery(schema, table, names, orderBy), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([][]any, 0)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make([]any, len(values))
		for i, value := range values {
			row[i] = jsonValue(value)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &models.RowsResponse{
		Columns: columns, Rows: result, Total: total, Limit: limit, Offset: offset,
	}, nil
}

// buildRowsQuery menyusun SELECT dengan semua identifier di-quote
// estimatedRows memakai reltuples untuk tabel besar agar viewer tidak selalu
// menjalankan COUNT(*) penuh. Tabel kecil/baru tetap memakai count eksak.
func estimatedRows(ctx context.Context, q querier, schema, table string) (int64, bool, error) {
	var reltuples float64
	var kind string
	err := q.QueryRow(ctx, `
		SELECT c.reltuples, c.relkind::text
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2`, schema, table).Scan(&reltuples, &kind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if (kind == "r" || kind == "p") && reltuples >= 10000 {
		return int64(reltuples), true, nil
	}
	return 0, false, nil
}

func buildRowsQuery(schema, table string, columns, orderBy []string) string {
	quoted := make([]string, len(columns))
	for i, col := range columns {
		quoted[i] = pgx.Identifier{col}.Sanitize()
	}
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(quoted, ", "), pgx.Identifier{schema, table}.Sanitize())
	if len(orderBy) > 0 {
		ordered := make([]string, len(orderBy))
		for i, col := range orderBy {
			ordered[i] = pgx.Identifier{col}.Sanitize()
		}
		query += " ORDER BY " + strings.Join(ordered, ", ")
	}
	return query + " LIMIT $1 OFFSET $2"
}

func primaryKeyColumns(ctx context.Context, q querier, schema, table string) ([]string, error) {
	var columns []string
	err := q.QueryRow(ctx, `
		SELECT COALESCE(array_agg(a.attname::text ORDER BY k.ord), '{}'::text[])
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		CROSS JOIN unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
		WHERE n.nspname = $1 AND c.relname = $2 AND con.contype = 'p'`, schema, table).Scan(&columns)
	return columns, err
}

func readTableColumns(ctx context.Context, q querier, schema, table string) ([]models.RowColumn, error) {
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
		)`, schema, table).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrTableNotFound
	}

	rows, err := q.Query(ctx, `
		SELECT a.attname::text, pg_catalog.format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make([]models.RowColumn, 0)
	for rows.Next() {
		var col models.RowColumn
		if err := rows.Scan(&col.Name, &col.Type); err != nil {
			return nil, err
		}
		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func jsonValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	case []byte:
		return string(v)
	case [16]byte:
		return formatUUID(v)
	case pgtype.Numeric:
		if !v.Valid {
			return nil
		}
		return driverValue(v)
	case pgtype.Date:
		if !v.Valid {
			return nil
		}
		return v.Time.Format("2006-01-02")
	case pgtype.Timestamp:
		if !v.Valid {
			return nil
		}
		return v.Time.UTC().Format(time.RFC3339Nano)
	case pgtype.Timestamptz:
		if !v.Valid {
			return nil
		}
		return v.Time.UTC().Format(time.RFC3339Nano)
	case pgtype.Text:
		if !v.Valid {
			return nil
		}
		return v.String
	case pgtype.UUID:
		if !v.Valid {
			return nil
		}
		return formatUUID(v.Bytes)
	}
	return value
}

func formatUUID(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func driverValue(value driver.Valuer) any {
	v, err := value.Value()
	if err != nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
