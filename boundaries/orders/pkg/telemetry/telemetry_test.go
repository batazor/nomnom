package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"

	"nomnom/orders/pkg/telemetry"
)

// Метрики Prometheus registry и OTel instruments собираются одним reader с общим resource.
func TestBridgesPrometheusRegistryIntoOneOTelPipeline(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=test,service.version=1.2.3")
	var reader *sdkmetric.ManualReader
	m, err := telemetry.New(context.Background(), "orders-consumer", func(producer sdkmetric.Producer) (sdkmetric.Reader, error) {
		reader = sdkmetric.NewManualReader(sdkmetric.WithProducer(producer))
		return reader, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	received := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "watermill", Subsystem: "consumer", Name: "subscriber_messages_received_total",
	}, []string{"acked"})
	m.Registry.MustRegister(received)
	received.WithLabelValues("acked").Inc()
	counter, err := m.Meter("test").Int64Counter("nomnom.messaging.retry.attempts")
	require.NoError(t, err)
	counter.Add(context.Background(), 1)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			names[metric.Name] = true
		}
	}
	assert.True(t, names["watermill_consumer_subscriber_messages_received_total"], "bridge metric: %v", names)
	assert.True(t, names["nomnom.messaging.retry.attempts"], "OTel instrument: %v", names)
	for _, want := range []string{"go.memory.used", "go.goroutine.count", "go.processor.limit", "process.cpu.time"} {
		assert.True(t, names[want], "runtime/process metric %s: %v", want, names)
	}
	for name := range names {
		assert.NotContains(t, name, "system.", "host metrics are dropped")
	}

	attrs := map[string]string{}
	for _, kv := range rm.Resource.Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	assert.Equal(t, "orders-consumer", attrs["service.name"])
	assert.Equal(t, "nomnom", attrs["service.namespace"])
	assert.Equal(t, "test", attrs["deployment.environment.name"])
	assert.Equal(t, "1.2.3", attrs["service.version"])
}

func TestEnvironmentOverridesServiceName(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "orders-consumer-canary")
	reader := sdkmetric.NewManualReader()
	m, err := telemetry.New(context.Background(), "orders-consumer", func(sdkmetric.Producer) (sdkmetric.Reader, error) { return reader, nil })
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	name, _ := rm.Resource.Set().Value("service.name")
	assert.Equal(t, "orders-consumer-canary", name.AsString())
}

func TestStartWithoutEndpointDoesNotExport(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	m, err := telemetry.Start(context.Background(), "orders-consumer")
	require.NoError(t, err)
	assert.Nil(t, m.TracerProvider)
	require.NoError(t, m.Shutdown(context.Background()))
}

// traceparent вызывающего продолжается в процессе, а spans экспортируются по OTLP.
func TestStartContinuesW3CTraceContextAndExportsTraces(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	m, err := telemetry.Start(context.Background(), "orders")
	require.NoError(t, err)
	require.NotNil(t, m.TracerProvider)

	carrier := propagation.MapCarrier{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	_, span := otel.Tracer("test").Start(ctx, "rpc")
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", span.SpanContext().TraceID().String())
	assert.True(t, trace.SpanContextFromContext(ctx).IsRemote())
	span.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = m.Shutdown(shutdownCtx) // collector недоступен: важна только ограниченность shutdown
}

// Counters Temporal SDK монотонные: в Prometheus — counter с _total, а не gauge.
func TestTemporalCountersAreMonotonic(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	m, err := telemetry.New(context.Background(), "orders-worker", func(sdkmetric.Producer) (sdkmetric.Reader, error) { return reader, nil })
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	handler := m.TemporalMetricsHandler()
	handler.Counter("temporal_workflow_completed").Inc(1)
	handler.Timer("temporal_activity_execution_latency").Record(20 * time.Millisecond)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	found := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != telemetry.TemporalMeterName {
			continue
		}
		for _, metric := range sm.Metrics {
			switch data := metric.Data.(type) {
			case metricdata.Sum[int64]:
				found[metric.Name] = true
				assert.True(t, data.IsMonotonic, metric.Name)
			case metricdata.Histogram[float64]:
				found[metric.Name] = true
				assert.Equal(t, telemetry.SecondsBuckets, data.DataPoints[0].Bounds, "seconds buckets for %s", metric.Name)
			}
		}
	}
	assert.True(t, found["temporal_workflow_completed"])
	assert.True(t, found["temporal_activity_execution_latency"])
}
