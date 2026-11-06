package telemetry

import (
	"context"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer         trace.Tracer
	tracerProvider *sdktrace.TracerProvider
	propagator     propagation.TextMapPropagator
)

// InitTracer initializes an OpenTelemetry tracer provider for the given service.
// It reads standard OTEL environment variables:
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP collector endpoint
//   - OTEL_TRACES_EXPORTER: exporter type (currently only "otlp" is supported)
//
// If no exporter endpoint is configured, it returns a no-op tracer provider so the
// application runs with zero overhead when telemetry is not enabled.
func InitTracer(serviceName string) (*sdktrace.TracerProvider, error) {
	propagator = propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	otel.SetTextMapPropagator(propagator)

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	exporterName := os.Getenv("OTEL_TRACES_EXPORTER")

	var tp *sdktrace.TracerProvider
	if endpoint == "" && (exporterName == "" || exporterName == "none") {
		// No telemetry configured: use a no-op provider.
		tp = sdktrace.NewTracerProvider()
	} else {
		var exporter sdktrace.SpanExporter
		var err error

		switch exporterName {
		case "otlp", "":
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			exporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint))
			if err != nil {
				return nil, err
			}
		default:
			// Unsupported exporter: fall back to no-op rather than failing startup.
			tp = sdktrace.NewTracerProvider()
			otel.SetTracerProvider(tp)
			tracer = tp.Tracer(serviceName)
			return tp, nil
		}

		res, err := sdkresource.Merge(
			sdkresource.Default(),
			sdkresource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String(serviceName),
			),
		)
		if err != nil {
			return nil, err
		}

		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
		)
	}

	otel.SetTracerProvider(tp)
	tracerProvider = tp
	tracer = tp.Tracer(serviceName)
	return tp, nil
}

// Tracer returns the global tracer for the initialized service.
func Tracer() trace.Tracer {
	if tracer == nil {
		return otel.Tracer("uninitialized")
	}
	return tracer
}

// Shutdown flushes and shuts down the tracer provider.
func Shutdown(ctx context.Context) error {
	if tracerProvider != nil {
		return tracerProvider.Shutdown(ctx)
	}
	return nil
}

// Extract returns a context with the trace context extracted from the HTTP headers.
func Extract(ctx context.Context, header http.Header) context.Context {
	if propagator == nil {
		return ctx
	}
	return propagator.Extract(ctx, propagation.HeaderCarrier(header))
}

// Inject injects the trace context from ctx into the HTTP headers.
func Inject(ctx context.Context, header http.Header) {
	if propagator == nil || ctx == nil {
		return
	}
	propagator.Inject(ctx, propagation.HeaderCarrier(header))
}

// InjectTraceparent injects the trace context into an outbound HTTP request.
// It is safe to call with a nil context or request.
func InjectTraceparent(ctx context.Context, req *http.Request) {
	if req == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	Inject(ctx, req.Header)
}

// HTTPTransport wraps an http.RoundTripper so that outbound requests carry the
// W3C traceparent header and are represented as child spans.
func HTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &tracingTransport{base: base}
}

type tracingTransport struct {
	base http.RoundTripper
}

func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	ctx, span := Tracer().Start(ctx, "HTTP "+req.Method)
	defer span.End()

	span.SetAttributes(
		semconv.HTTPMethodKey.String(req.Method),
		semconv.HTTPURLKey.String(req.URL.String()),
	)

	Inject(ctx, req.Header)
	req = req.WithContext(ctx)

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		return resp, err
	}
	span.SetAttributes(semconv.HTTPStatusCodeKey.Int(resp.StatusCode))
	return resp, nil
}
