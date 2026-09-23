package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// TODO: Integrate the OpenTelemetry metrics SDK to expose RED metricsS
// in Prometheus format on the /metrics endpoint to enable Grafana alerting.

// O11y manages TraceID generation and RED metrics logging.
func O11y(next http.Handler) http.Handler {
	tracer := otel.Tracer("tesseract-api-gateway")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, r.URL.Path)
		defer span.End()

		rw := &responseWriterObserver{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r.WithContext(ctx))

		duration := time.Since(start)
		traceID := span.SpanContext().TraceID().String()

		slog.Info("HTTP Request Processed",
			slog.String("trace_id", traceID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Duration("latency", duration),
		)
	})
}

// responseWriterObserver is a decorator pattern used to capture the status code
// without breaking the standard http.ResponseWriter interface contract.
type responseWriterObserver struct {
	http.ResponseWriter
	status int
}

// WriteHeader overrides the standard method to record the HTTP status code before writing it.
func (o *responseWriterObserver) WriteHeader(code int) {
	o.status = code
	o.ResponseWriter.WriteHeader(code)
}
