package databasehandler

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"
	"web_backend/internal/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrTableNotFound = errors.New("table not found")

func readSchema(ctx context.Context, pool *pgxpool.Pool) ([]models.SchemaInfo, error) {
	rows, err := pool.Query(ctx, `
		SELECT n.nspname
		FROM pg_namespace n
		WHERE n.nspname NOT LIKE 'pg_%' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schemas := make([]models.SchemaInfo, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		schema := models.SchemaInfo{Name: name, Tables: []models.SchemaTable{}}
		tables, err := readTables(ctx, pool, name)
		if err != nil {
			return nil, err
		}
		schema.Tables = tables
		schemas = append(schemas, schema)
	}
	return schemas, rows.Err()
}

func readTables(ctx context.Context, pool *pgxpool.Pool, schema string) ([]models.SchemaTable, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.relname, c.relkind
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm')
		ORDER BY c.relname`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := make([]models.SchemaTable, 0)
	for rows.Next() {
		var name string
		var kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, err
		}
		tableType := "table"
		if kind == "v" || kind == "m" {
			tableType = "view"
		}
		table := models.SchemaTable{
			Name: name, Type: tableType,
			Columns: []models.SchemaColumn{}, Constraints: []models.SchemaConstraint{}, Indexes: []models.SchemaIndex{},
		}
		if table.Columns, err = readColumns(ctx, pool, schema, name); err != nil {
			return nil, err
		}
		if table.Constraints, err = readConstraints(ctx, pool, schema, name); err != nil {
			return nil, err
		}
		if table.Indexes, err = readIndexes(ctx, pool, schema, name); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func readColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]models.SchemaColumn, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.attname,
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

func readConstraints(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]models.SchemaConstraint, error) {
	rows, err := pool.Query(ctx, `
		SELECT con.conname, con.contype, pg_get_constraintdef(con.oid)
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

func readIndexes(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]models.SchemaIndex, error) {
	rows, err := pool.Query(ctx, `
		SELECT i.relname, ix.indisunique, pg_get_indexdef(ix.indexrelid)
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

func readRows(ctx context.Context, pool *pgxpool.Pool, schema, table string, limit, offset int) (*models.RowsResponse, error) {
	columns, err := readTableColumns(ctx, pool, schema, table)
	if err != nil {
		return nil, err
	}

	ident := pgx.Identifier{schema, table}.Sanitize()
	var total int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+ident).Scan(&total); err != nil {
		return nil, err
	}

	quoted := make([]string, len(columns))
	for i, col := range columns {
		quoted[i] = pgx.Identifier{col.Name}.Sanitize()
	}
	query := fmt.Sprintf("SELECT %s FROM %s LIMIT $1 OFFSET $2", strings.Join(quoted, ", "), ident)
	rows, err := pool.Query(ctx, query, limit, offset)
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

func readTableColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]models.RowColumn, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
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

	rows, err := pool.Query(ctx, `
		SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod)
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
		return fmt.Sprintf("%x-%x-%x-%x-%x", v.Bytes[0:4], v.Bytes[4:6], v.Bytes[6:8], v.Bytes[8:10], v.Bytes[10:16])
	}
	return value
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
