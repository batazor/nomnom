package telemetry_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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

	attrs := map[string]string{}
	for _, kv := range rm.Resource.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
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
	m, err := telemetry.Start(context.Background(), "orders-consumer")
	require.NoError(t, err)
	require.NoError(t, m.Shutdown(context.Background()))
}
