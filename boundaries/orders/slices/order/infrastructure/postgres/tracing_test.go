package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"nomnom/orders/pkg/telemetry"
)

// installProviders registers the process telemetry pipeline (views included)
// and a span recorder as the global providers the pgx tracer resolves.
func installProviders(t *testing.T) (*sdkmetric.ManualReader, *tracetest.SpanRecorder) {
	t.Helper()
	var reader *sdkmetric.ManualReader
	m, err := telemetry.New(context.Background(), "orders", func(producer sdkmetric.Producer) (sdkmetric.Reader, error) {
		reader = sdkmetric.NewManualReader(sdkmetric.WithProducer(producer))
		return reader, nil
	})
	require.NoError(t, err)
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	previousMeter, previousTracer := otel.GetMeterProvider(), otel.GetTracerProvider()
	otel.SetMeterProvider(m.MeterProvider)
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetMeterProvider(previousMeter)
		otel.SetTracerProvider(previousTracer)
		_ = m.Shutdown(context.Background())
		_ = tp.Shutdown(context.Background())
	})
	return reader, spans
}

// Query spans carry semconv attributes but neither the statement nor bind
// parameters; db.client.operation.duration keeps only the inventory labels and
// otelpgx's own error counter is dropped.
func TestQueryTracerRecordsSafeSpansAndSemconvDuration(t *testing.T) {
	reader, spans := installProviders(t)
	tracer := connTracer(&poolMetrics{})

	parent, root := otel.Tracer("test").Start(context.Background(), "usecase get_order")
	const sql = "SELECT state FROM orders WHERE order_id = $1"
	ctx := tracer.TraceQueryStart(parent, nil, pgx.TraceQueryStartData{SQL: sql, Args: []any{"secret-order-id"}})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})
	ctx = tracer.TraceQueryStart(parent, nil, pgx.TraceQueryStartData{SQL: "UPDATE orders SET state = $1"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("boom")})
	root.End()

	var query []sdktrace.ReadOnlySpan
	for _, s := range spans.Ended() {
		if s.SpanKind() == trace.SpanKindClient {
			query = append(query, s)
		}
	}
	require.Len(t, query, 2)
	assert.Equal(t, "SELECT", query[0].Name())
	assert.Equal(t, root.SpanContext().SpanID(), query[0].Parent().SpanID())
	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range query[0].Attributes() {
		attrs[kv.Key] = kv.Value
	}
	assert.Equal(t, "postgresql", attrs["db.system.name"].AsString())
	assert.NotContains(t, attrs, attribute.Key("db.query.text"), "statement text is disabled")
	assert.NotContains(t, attrs, attribute.Key("pgx.query.parameters"), "bind parameters are never recorded")

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var duration *metricdata.Histogram[float64]
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			assert.NotEqual(t, "db.client.operation.errors", m.Name, "non-semconv metric is dropped")
			if m.Name == telemetry.DBOperationDurationMetric {
				h := m.Data.(metricdata.Histogram[float64])
				duration = &h
			}
		}
	}
	require.NotNil(t, duration, "db.client.operation.duration")
	operations := map[string]bool{}
	allowed := map[attribute.Key]bool{}
	for _, k := range telemetry.DBOperationDurationAttributes {
		allowed[k] = true
	}
	for _, point := range duration.DataPoints {
		for _, kv := range point.Attributes.ToSlice() {
			assert.True(t, allowed[kv.Key], "label %s is not in the inventory", kv.Key)
		}
		name, _ := point.Attributes.Value("db.operation.name")
		operations[name.AsString()] = true
	}
	assert.Equal(t, map[string]bool{"SELECT": true, "UPDATE": true}, operations)
}

// Without an active trace pgx creates no root spans of its own.
func TestQueryTracerCreatesNoRootSpans(t *testing.T) {
	_, spans := installProviders(t)
	tracer := connTracer(&poolMetrics{})
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	assert.Empty(t, spans.Ended())
}
