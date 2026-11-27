package databasehandler

import (
	"context"
	"web_backend/internal/models"

	"github.com/jackc/pgx/v5/pgtype"
)

// readColumnsBySchema mengambil kolom semua tabel dalam satu schema sekaligus
// untuk menghindari N+1 per tabel.
func readColumnsBySchema(ctx context.Context, q querier, schema string) (map[string][]models.SchemaColumn, error) {
	rows, err := q.Query(ctx, `
		SELECT c.relname::text, a.attname::text,
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
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm')
		  AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY c.relname, a.attnum`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]models.SchemaColumn)
	for rows.Next() {
		var table string
		var col models.SchemaColumn
		var def pgtype.Text
		if err := rows.Scan(&table, &col.Name, &col.Type, &col.Nullable, &def, &col.IsPrimaryKey); err != nil {
			return nil, err
		}
		if def.Valid {
			value := def.String
			col.Default = &value
		}
		out[table] = append(out[table], col)
	}
	return out, rows.Err()
}

func readConstraintsBySchema(ctx context.Context, q querier, schema string) (map[string][]models.SchemaConstraint, error) {
	rows, err := q.Query(ctx, `
		SELECT c.relname::text, con.conname::text, con.contype::text, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm')
		  AND con.contype IN ('p', 'f', 'u', 'c')
		ORDER BY c.relname, con.conname`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]models.SchemaConstraint)
	for rows.Next() {
		var table, kind string
		var item models.SchemaConstraint
		if err := rows.Scan(&table, &item.Name, &kind, &item.Definition); err != nil {
			return nil, err
		}
		item.Type = constraintType(kind)
		out[table] = append(out[table], item)
	}
	return out, rows.Err()
}

func readIndexesBySchema(ctx context.Context, q querier, schema string) (map[string][]models.SchemaIndex, error) {
	rows, err := q.Query(ctx, `
		SELECT c.relname::text, i.relname::text, ix.indisunique, pg_get_indexdef(ix.indexrelid)
		FROM pg_index ix
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_class c ON c.oid = ix.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'v', 'm')
		ORDER BY c.relname, i.relname`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]models.SchemaIndex)
	for rows.Next() {
		var table string
		var item models.SchemaIndex
		if err := rows.Scan(&table, &item.Name, &item.IsUnique, &item.Definition); err != nil {
			return nil, err
		}
		out[table] = append(out[table], item)
	}
	return out, rows.Err()
}
