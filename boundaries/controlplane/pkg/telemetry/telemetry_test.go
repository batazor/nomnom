package telemetry_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/telemetry"
)

// Runtime- и process-метрики собираются с resource процесса; host-метрики отброшены.
func TestCollectsRuntimeMetricsWithTheProcessResource(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=test,service.version=1.2.3")
	reader := sdkmetric.NewManualReader()
	tel, err := telemetry.New(context.Background(), "controlplane", func() (sdkmetric.Reader, error) { return reader, nil })
	require.NoError(t, err)
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	counter, err := tel.Meter("test").Int64Counter("nomnom.test.calls")
	require.NoError(t, err)
	counter.Add(context.Background(), 1)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	for _, want := range []string{"nomnom.test.calls", "go.memory.used", "process.cpu.time"} {
		assert.True(t, names[want], "metric %s: %v", want, names)
	}
	for name := range names {
		assert.NotContains(t, name, "system.", "host metrics are dropped")
	}
	attrs := map[string]string{}
	for _, kv := range rm.Resource.Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	assert.Equal(t, "controlplane", attrs["service.name"])
	assert.Equal(t, "nomnom", attrs["service.namespace"])
	assert.Equal(t, "1.2.3", attrs["service.version"])
	assert.Equal(t, "test", attrs["deployment.environment.name"])
}
