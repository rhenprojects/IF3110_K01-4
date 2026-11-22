package engine

import (
	"web_backend/internal/models"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// tracer memakai TracerProvider global (dipasang telemetry.Setup)
var tracer = otel.Tracer("web_backend/internal/engine")

// maxQueryAttr membatasi panjang SQL yang ikut dicatat di span.
const maxQueryAttr = 2048

func jobAttrs(job *models.MigrationJob) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("migration.job_id", job.ID),
		attribute.String("migration.execution_id", job.ExecutionID),
		attribute.String("migration.id", job.MigrationID),
		attribute.String("migration.kind", string(job.Kind)),
		attribute.String("db.connection_id", job.ConnectionID),
		attribute.Int("migration.attempt", job.Attempts),
	}
}

func specAttrs(spec attemptSpec) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("migration.execution_id", spec.executionID),
		attribute.String("migration.id", spec.migrationID),
		attribute.String("migration.kind", string(spec.kind)),
		attribute.String("db.connection_id", spec.connectionID),
		attribute.Int("migration.attempt", spec.attempt),
	}
}

func queryText(sql string) string {
	sql = redactSQL(sql)
	if len(sql) > maxQueryAttr {
		return sql[:maxQueryAttr] + "…"
	}
	return sql
}

// endSpan menandai span gagal bila err tidak nil, lalu menutupnya.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
