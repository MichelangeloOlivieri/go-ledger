package telemetry

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

// configures the OpenTelemetry RED metrics provider
func InitTracer(ctx context.Context, serviceName string) (*sdktrace.TracerProvider, error) {
	var tpOptions []sdktrace.TracerProviderOption

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	)
	tpOptions = append(tpOptions, sdktrace.WithResource(res))

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	// graceful degradation
	if endpoint != "" {
		opts := []otlptracehttp.Option{
			otlptracehttp.WithInsecure(),
			otlptracehttp.WithEndpoint(endpoint),
		}
		exporter, err := otlptracehttp.New(ctx, opts...)
		if err != nil {
			return nil, err
		}

		tpOptions = append(tpOptions, sdktrace.WithBatcher(exporter))
	}

	tp := sdktrace.NewTracerProvider(tpOptions...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp, nil
}
