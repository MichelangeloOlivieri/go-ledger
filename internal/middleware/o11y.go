package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// TODO: integrate OpenTelemetry's SDK to expose RED metrics in Prometheus format to enable Grafana alerting

// captures the status code without breaking the standard http.ResponseWriter interface contract
type responseWriterObserver struct {
	http.ResponseWriter
	status int
}

// integrates the standard method to record the HTTP status code
func (o *responseWriterObserver) WriteHeader(code int) {
	o.status = code
	o.ResponseWriter.WriteHeader(code)
}

// manages RED metrics logging
func O11y(nextHandler http.Handler) http.Handler {

	otelHandler := func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		observer := &responseWriterObserver{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		nextHandler.ServeHTTP(observer, r)

		duration := time.Since(start)

		// logs linked to each single HTTP request
		slog.InfoContext(r.Context(),
			"HTTP Request Processed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			// could not be doing the following without saving the status manually
			slog.Int("status", observer.status),
			slog.Duration("latency", duration),
		)
	}

	return http.HandlerFunc(otelHandler)
}
