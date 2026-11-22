package telemetry

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var httpTracer = otel.Tracer("web_backend/http")

// Middleware membuka satu server span per request, menaruhnya di UserContext untuk handler berikutnya, lalu menulis access log JSON yang membawa trace_id.
func Middleware(logger *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		header := http.Header{}
		c.Request().Header.VisitAll(func(k, v []byte) { header.Add(string(k), string(v)) })
		ctx := otel.GetTextMapPropagator().Extract(c.UserContext(), propagation.HeaderCarrier(header))

		ctx, span := httpTracer.Start(ctx, c.Method(), trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", c.Method()), attribute.String("url.path", c.Path())))
		defer span.End()
		c.SetUserContext(ctx)
		c.Set("traceparent", Inject(ctx))

		start := time.Now()
		err := c.Next()
		if err != nil {
			if herr := c.App().ErrorHandler(c, err); herr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}

		status := c.Response().StatusCode()
		route := c.Route().Path
		span.SetName(c.Method() + " " + route)
		span.SetAttributes(attribute.String("http.route", route), attribute.Int("http.response.status_code", status))
		level := slog.LevelInfo
		if status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(status))
			level = slog.LevelError
		}
		logger.Log(ctx, level, "http request", "event", "http.request", "method", c.Method(), "route", route,
			"path", c.Path(), "status", status, "duration_ms", time.Since(start).Milliseconds(), "ip", c.IP())
		return nil
	}
}
