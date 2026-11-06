package telemetry

import (
	"context"
	"log"
	"net/http"
	"os"
	"sync/atomic"
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
	tracer         atomic.Value // stores trace.Tracer
	tracerProvider atomic.Pointer[sdktrace.TracerProvider]
	propagator     propagation.TextMapPropagator
)

// InitTracer initializes an OpenTelemetry tracer provider for the given service.
// It reads standard OTEL environment variables:
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP collector endpoint
//   - OTEL_TRACES_EXPORTER: exporter type (currently only "otlp" is supported)
//
// If telemetry is not configured, a no-op provider is installed.
// If telemetry is configured, a no-op provider is installed immediately so that
// startup is never blocked, and the real exporter/provider is built in a
// background goroutine. Once ready, the global provider is swapped atomically.
func InitTracer(serviceName string) (*sdktrace.TracerProvider, error) {
	propagator = propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	otel.SetTextMapPropagator(propagator)

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	exporterName := os.Getenv("OTEL_TRACES_EXPORTER")

	// Start with a no-op provider so the application can boot without waiting
	// for the OTLP collector to be reachable.
	tp := sdktrace.NewTracerProvider()
	tracerProvider.Store(tp)
	otel.SetTracerProvider(tp)
	setTracer(tp.Tracer(serviceName))

	if endpoint == "" && (exporterName == "" || exporterName == "none") {
		return tp, nil
	}

	if exporterName != "" && exporterName != "otlp" {
		log.Printf("unsupported OTEL_TRACES_EXPORTER=%q; using no-op tracer provider", exporterName)
		return tp, nil
	}

	// Build the real exporter/provider in the background to avoid blocking
	// application startup on collector connectivity.
	go func() {
		realTP, err := buildTracerProvider(serviceName, endpoint)
		if err != nil {
			log.Printf("failed to initialize OTLP tracer provider: %v; keeping no-op provider", err)
			return
		}
		old := tracerProvider.Swap(realTP)
		if old != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = old.Shutdown(ctx)
			cancel()
		}
		otel.SetTracerProvider(realTP)
		setTracer(realTP.Tracer(serviceName))
	}()

	return tp, nil
}

func buildTracerProvider(serviceName, endpoint string) (*sdktrace.TracerProvider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint))
	if err != nil {
		return nil, err
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

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	), nil
}

func setTracer(t trace.Tracer) {
	if t == nil {
		t = otel.Tracer("uninitialized")
	}
	tracer.Store(t)
}

// Tracer returns the global tracer for the initialized service.
func Tracer() trace.Tracer {
	if t, ok := tracer.Load().(trace.Tracer); ok && t != nil {
		return t
	}
	return otel.Tracer("uninitialized")
}

// Shutdown flushes and shuts down the tracer provider.
func Shutdown(ctx context.Context) error {
	if tp := tracerProvider.Load(); tp != nil {
		return tp.Shutdown(ctx)
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
