// Package telemetry initialises OpenTelemetry tracing and W3C context propagation. Exporting is opt-in through
// standard OTEL_* environment variables; an unset endpoint keeps a no-op provider so local runs need no collector.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// Init installs the global tracer provider and propagator. The returned shutdown flushes spans.
func Init(ctx context.Context, service, environment, version string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	}
	if endpoint == "" || strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(service),
			semconv.ServiceVersion(version),
			semconv.DeploymentEnvironmentName(environment),
			attribute.String("service.namespace", "agent-platform"),
		),
	)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplerRatio()))),
	)
	otel.SetTracerProvider(provider)
	return func(shutdownCtx context.Context) error {
		ctx, cancel := context.WithTimeout(shutdownCtx, 10*time.Second)
		defer cancel()
		return provider.Shutdown(ctx)
	}, nil
}

func samplerRatio() float64 {
	raw := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG"))
	if raw == "" {
		return 1
	}
	var value float64
	if _, err := fmt.Sscanf(raw, "%f", &value); err != nil || value < 0 || value > 1 {
		return 1
	}
	return value
}

// Validate is exposed for tests and configuration diagnostics.
func Validate() error {
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" && !strings.Contains(endpoint, "://") {
		return errors.New("OTEL_EXPORTER_OTLP_ENDPOINT must include a scheme")
	}
	return nil
}

// TraceparentMiddleware exposes the current span as a W3C traceparent header for downstream command forwarding and
// outbox persistence. Incoming trace context has already been extracted by the HTTP instrumentation.
func TraceparentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanContextFromContext(r.Context())
		if span.IsValid() {
			r.Header.Set("traceparent", "00-"+span.TraceID().String()+"-"+span.SpanID().String()+"-01")
		}
		next.ServeHTTP(w, r)
	})
}
