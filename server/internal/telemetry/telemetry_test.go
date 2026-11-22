package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"web_backend/internal/telemetry"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func setup(t *testing.T) {
	t.Helper()
	shutdown, err := telemetry.Setup("telemetry-test", io.Discard)
	if err != nil {

		t.Fatal(err)

	}
	t.Cleanup(func(){ _ = shutdown(context.Background()) })
}

func TestInjectExtractRoundTrip(t *testing.T) {

	setup(t)
	ctx, span := otel.Tracer("test").Start(context.Background(), "root")

	defer span.End()

	tp := telemetry.Inject(ctx)
	want := span.SpanContext()
	if !strings.HasPrefix(tp, "00-"+want.TraceID().String()+"-"+want.SpanID().String()) {
		t.Fatalf("traceparent = %q, want trace %s span %s", tp, want.TraceID(), want.SpanID())
	}
	got := trace.SpanContextFromContext(telemetry.Extract(context.Background(), tp))
	if got.TraceID() != want.TraceID() || got.SpanID() != want.SpanID() || !got.IsRemote() {
		t.Fatalf("extracted %+v, want %+v", got, want)
	}

	base := context.Background()
	if telemetry.Extract(base, "") != base {
		t.Error("empty traceparent should leave context unchanged")
	}
}

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func TestLoggerAddsTraceContext(t *testing.T) {
	setup(t)
	var buf bytes.Buffer
	logger := telemetry.NewLogger(&buf).With("component", "test")

	ctx, span := otel.Tracer("test").Start(context.Background(), "root")
	logger.InfoContext(ctx, "inside span")
	span.End()
	logger.Info("outside span")

	lines := decodeLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("got %d log lines", len(lines))
	}
	sc := span.SpanContext()
	if lines[0]["trace_id"] != sc.TraceID().String() || lines[0]["span_id"] != sc.SpanID().String() {
		t.Errorf("log inside span = %v, want trace %s span %s", lines[0], sc.TraceID(), sc.SpanID())
	}
	if lines[0]["component"] != "test" {
		t.Errorf("With attributes lost: %v", lines[0])
	}
	if _, ok := lines[1]["trace_id"]; ok {
		t.Errorf("log outside span should have no trace_id: %v", lines[1])
	}
}

func TestMiddlewareContinuesIncomingTrace(t *testing.T) {
	setup(t)
	var buf bytes.Buffer
	app := fiber.New()
	app.Use(telemetry.Middleware(telemetry.NewLogger(&buf)))
	var seen trace.SpanContext
	app.Get("/items/:id", func(c *fiber.Ctx) error {
		seen = trace.SpanContextFromContext(c.UserContext())
		return c.SendStatus(fiber.StatusTeapot)
	})

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest("GET", "/items/7", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}

	if seen.TraceID().String() != traceID {
		t.Errorf("handler trace = %s, want %s", seen.TraceID(), traceID)
	}
	if got := resp.Header.Get("traceparent"); !strings.Contains(got, traceID) {
		t.Errorf("response traceparent = %q", got)
	}
	lines := decodeLines(t, &buf)
	last := lines[len(lines)-1]
	if last["event"] != "http.request" || last["route"] != "/items/:id" || last["status"] != float64(fiber.StatusTeapot) || last["trace_id"] != traceID {
		t.Errorf("access log = %v", last)
	}
	
}
