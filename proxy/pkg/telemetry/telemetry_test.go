package telemetry

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestInitTracer_NoOpWithoutEnv(t *testing.T) {
	tp, err := InitTracer("test-service")
	require.NoError(t, err)
	require.NotNil(t, tp)

	ctx, span := Tracer().Start(context.Background(), "test-span")
	assert.NotNil(t, ctx)
	assert.NotNil(t, span)
	span.End()
}

func TestInjectExtract_RoundTrip(t *testing.T) {
	_, err := InitTracer("test-service")
	require.NoError(t, err)

	ctx, span := Tracer().Start(context.Background(), "test-span")
	defer span.End()

	header := http.Header{}
	Inject(ctx, header)

	extractedCtx := Extract(context.Background(), header)
	extractedSpan := trace.SpanFromContext(extractedCtx)
	assert.Equal(t, span.SpanContext().TraceID(), extractedSpan.SpanContext().TraceID())
}

func TestInitTracer_ReturnsImmediatelyWithUnreachableEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:1")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")

	start := time.Now()
	tp, err := InitTracer("test-service")
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, tp)
	assert.Less(t, elapsed, 500*time.Millisecond, "InitTracer should not block on an unreachable OTLP collector")
}
