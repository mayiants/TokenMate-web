package telemetry

import (
	"context"
	"net/http"
	"testing"

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
