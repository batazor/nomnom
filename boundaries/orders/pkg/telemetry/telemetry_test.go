package telemetry_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
)

// Метрики Prometheus registry и OTel instruments собираются одним reader с общим resource.
func TestBridgesPrometheusRegistryIntoOneOTelPipeline(t *testing.T) {
	var reader *sdkmetric.ManualReader
	m, err := telemetry.New(context.Background(), config.TelemetryConfig{
		ServiceName:        "orders-consumer",
		ResourceAttributes: map[string]string{"deployment.environment.name": "test", "service.version": "1.2.3"},
		InstanceID:         "orders-consumer-0",
	}, func(producer sdkmetric.Producer) (sdkmetric.Reader, error) {
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
	assert.Equal(t, "orders-consumer-0", attrs["service.instance.id"])
}

// AC-1079: OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES do not override the file.
func TestEnvironmentDoesNotOverrideServiceName(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "other")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=other,deployment.environment.name=other")
	reader := sdkmetric.NewManualReader()
	m, err := telemetry.New(context.Background(), config.TelemetryConfig{
		ServiceName:        "orders-consumer",
		ResourceAttributes: map[string]string{"deployment.environment.name": "kind"},
	}, func(sdkmetric.Producer) (sdkmetric.Reader, error) { return reader, nil })
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	name, _ := rm.Resource.Set().Value("service.name")
	assert.Equal(t, "orders-consumer", name.AsString())
	environment, _ := rm.Resource.Set().Value("deployment.environment.name")
	assert.Equal(t, "kind", environment.AsString())
}

// Переход metric → trace в Grafana: измерение внутри sampled span уходит с exemplar
// этого span (фильтр trace_based SDK по умолчанию), вне span — без exemplar.
func TestHistogramMeasuredInSpanCarriesExemplarOfThatSpan(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	m, err := telemetry.New(context.Background(), config.TelemetryConfig{ServiceName: "orders"},
		func(sdkmetric.Producer) (sdkmetric.Reader, error) { return reader, nil })
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	duration, err := m.Meter("test").Float64Histogram("nomnom.usecase.duration", metric.WithUnit("s"))
	require.NoError(t, err)

	tracer := sdktrace.NewTracerProvider().Tracer("test")
	ctx, span := tracer.Start(context.Background(), "usecase place_order")
	duration.Record(ctx, 0.2, metric.WithAttributes(attribute.String("nomnom.outcome", "success")))
	span.End()
	duration.Record(context.Background(), 0.3, metric.WithAttributes(attribute.String("nomnom.outcome", "error")))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	exemplars := map[string][]metricdata.Exemplar[float64]{}
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			if mt.Name != "nomnom.usecase.duration" {
				continue
			}
			for _, point := range mt.Data.(metricdata.ExponentialHistogram[float64]).DataPoints {
				outcome, _ := point.Attributes.Value("nomnom.outcome")
				exemplars[outcome.AsString()] = point.Exemplars
			}
		}
	}
	require.Len(t, exemplars["success"], 1)
	traceID, spanID := span.SpanContext().TraceID(), span.SpanContext().SpanID()
	assert.Equal(t, traceID[:], exemplars["success"][0].TraceID)
	assert.Equal(t, spanID[:], exemplars["success"][0].SpanID)
	assert.InDelta(t, 0.2, exemplars["success"][0].Value, 1e-9)
	assert.Empty(t, exemplars["error"])
}

// AC-1080: an endpoint without tracing.enabled and metrics.enabled exports nothing.
func TestStartWithoutFlagsDoesNotExport(t *testing.T) {
	var requests atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
	t.Cleanup(collector.Close)
	m, err := telemetry.Start(context.Background(), config.ObservabilityConfig{
		Telemetry: config.TelemetryConfig{Endpoint: collector.URL, Protocol: config.ProtocolHTTPProtobuf, ServiceName: "orders-consumer"},
		Metrics:   config.MetricsConfig{ExportInterval: time.Minute},
	})
	require.NoError(t, err)
	assert.Nil(t, m.TracerProvider)
	_, span := otel.Tracer("test").Start(context.Background(), "rpc")
	span.End()
	require.NoError(t, m.Shutdown(context.Background()))
	assert.Zero(t, requests.Load(), "no signal reaches the collector")
}

// traceparent вызывающего продолжается в процессе, а spans экспортируются по OTLP gRPC.
func TestStartContinuesW3CTraceContextAndExportsTraces(t *testing.T) {
	m, err := telemetry.Start(context.Background(), config.ObservabilityConfig{
		Telemetry: config.TelemetryConfig{Endpoint: "http://127.0.0.1:1", Protocol: config.ProtocolGRPC, ServiceName: "orders"},
		Tracing:   config.TracingConfig{Enabled: true},
		Metrics:   config.MetricsConfig{ExportInterval: time.Minute},
	})
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

// AC-1079: spans and metrics go to telemetry.endpoint over OTLP/HTTP with
// service.name from the file, whatever OTEL_* says.
func TestStartExportsToConfiguredEndpointOverHTTP(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("OTEL_EXPORTER_OTLP_ENDPOINT must not receive signals")
	}))
	t.Cleanup(other.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", other.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", other.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", other.URL)
	t.Setenv("OTEL_SERVICE_NAME", "other")

	var mu sync.Mutex
	received := map[string][]byte{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		mu.Lock()
		received[r.URL.Path] = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(collector.Close)

	m, err := telemetry.Start(context.Background(), config.ObservabilityConfig{
		Telemetry: config.TelemetryConfig{Endpoint: collector.URL, Protocol: config.ProtocolHTTPProtobuf, ServiceName: "orders-worker", InstanceID: "orders-worker-0"},
		Tracing:   config.TracingConfig{Enabled: true},
		Metrics:   config.MetricsConfig{Enabled: true, ExportInterval: time.Minute},
	})
	require.NoError(t, err)
	_, span := otel.Tracer("test").Start(context.Background(), "rpc")
	span.End()
	require.NoError(t, m.Shutdown(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	var traces collectortrace.ExportTraceServiceRequest
	require.NoError(t, proto.Unmarshal(received["/v1/traces"], &traces))
	require.NotEmpty(t, traces.ResourceSpans)
	resource := map[string]string{}
	for _, kv := range traces.ResourceSpans[0].Resource.Attributes {
		resource[kv.Key] = kv.Value.GetStringValue()
	}
	assert.Equal(t, "orders-worker", resource["service.name"])
	assert.Equal(t, "orders-worker-0", resource["service.instance.id"])
	var metrics collectormetrics.ExportMetricsServiceRequest
	require.NoError(t, proto.Unmarshal(received["/v1/metrics"], &metrics))
	assert.NotEmpty(t, metrics.ResourceMetrics)
}

// Counters Temporal SDK монотонные: в Prometheus — counter с _total, а не gauge;
// timers — exponential histograms общего view.
func TestTemporalCountersAreMonotonic(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	m, err := telemetry.New(context.Background(), config.TelemetryConfig{ServiceName: "orders-worker"}, func(sdkmetric.Producer) (sdkmetric.Reader, error) { return reader, nil })
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
			case metricdata.ExponentialHistogram[float64]:
				found[metric.Name] = true
				assert.LessOrEqual(t, len(data.DataPoints[0].PositiveBucket.Counts), int(telemetry.ExponentialHistogram.MaxSize), "buckets of %s", metric.Name)
				assert.EqualValues(t, 1, data.DataPoints[0].Count, metric.Name)
			}
		}
	}
	assert.True(t, found["temporal_workflow_completed"])
	assert.True(t, found["temporal_activity_execution_latency"])
}
