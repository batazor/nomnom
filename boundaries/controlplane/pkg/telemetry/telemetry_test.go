package telemetry_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/telemetry"
)

// telemetryConfig — валидная конфигурация telemetry процесса с экспортом на endpoint.
func telemetryConfig(endpoint, protocol string, enabled bool) config.Config {
	return config.Config{
		Telemetry: config.TelemetryConfig{
			Endpoint:           endpoint,
			Protocol:           protocol,
			ServiceName:        "controlplane-blue",
			ResourceAttributes: map[string]string{"deployment.environment.name": "test", "service.version": "1.2.3"},
			InstanceID:         "controlplane-0",
		},
		Tracing: config.TracingConfig{Enabled: enabled},
		// Финальный export — на Shutdown, а не по таймеру.
		Metrics: config.MetricsConfig{Enabled: enabled, ExportInterval: time.Hour},
	}
}

// Fake OTLP/gRPC коллектор: принятые запросы складываются в каналы.
type traceCollector struct {
	coltracepb.UnimplementedTraceServiceServer
	requests chan *coltracepb.ExportTraceServiceRequest
}

func (c *traceCollector) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	c.requests <- req
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

type metricCollector struct {
	colmetricpb.UnimplementedMetricsServiceServer
	requests chan *colmetricpb.ExportMetricsServiceRequest
}

func (c *metricCollector) Export(_ context.Context, req *colmetricpb.ExportMetricsServiceRequest) (*colmetricpb.ExportMetricsServiceResponse, error) {
	c.requests <- req
	return &colmetricpb.ExportMetricsServiceResponse{}, nil
}

func startGRPCCollector(t *testing.T) (string, *traceCollector, *metricCollector) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	traces := &traceCollector{requests: make(chan *coltracepb.ExportTraceServiceRequest, 64)}
	metrics := &metricCollector{requests: make(chan *colmetricpb.ExportMetricsServiceRequest, 64)}
	server := grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(server, traces)
	colmetricpb.RegisterMetricsServiceServer(server, metrics)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return "http://" + listener.Addr().String(), traces, metrics
}

func attributes(kvs []*commonpb.KeyValue) map[string]string {
	attrs := map[string]string{}
	for _, kv := range kvs {
		attrs[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	return attrs
}

func emitSignals(t *testing.T, tel *telemetry.Telemetry) {
	t.Helper()
	if tel.TracerProvider != nil {
		_, span := tel.TracerProvider.Tracer("test").Start(context.Background(), "usecase publish_routes")
		span.End()
	}
	counter, err := tel.Meter("test").Int64Counter("nomnom.test.calls")
	require.NoError(t, err)
	counter.Add(context.Background(), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, tel.Shutdown(ctx))
}

// AC-1079: spans и метрики уходят на telemetry.endpoint с service.name из файла,
// OTEL_* не действуют.
func TestStartExportsOverGRPCToTheConfiguredEndpoint(t *testing.T) {
	endpoint, traces, metrics := startGRPCCollector(t)
	otherEndpoint, otherTraces, otherMetrics := startGRPCCollector(t)
	for env, value := range map[string]string{
		"OTEL_SERVICE_NAME":                   "other",
		"OTEL_RESOURCE_ATTRIBUTES":            "service.instance.id=other",
		"OTEL_EXPORTER_OTLP_ENDPOINT":         otherEndpoint,
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  otherEndpoint,
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": otherEndpoint,
		"OTEL_TRACES_SAMPLER":                 "always_off",
	} {
		t.Setenv(env, value)
	}

	tel, err := telemetry.Start(context.Background(), telemetryConfig(endpoint, config.TelemetryProtocolGRPC, true))
	require.NoError(t, err)
	emitSignals(t, tel)

	require.NotEmpty(t, traces.requests, "spans reach telemetry.endpoint")
	traceResource := attributes((<-traces.requests).GetResourceSpans()[0].GetResource().GetAttributes())
	assert.Equal(t, "controlplane-blue", traceResource["service.name"])
	assert.Equal(t, "controlplane-0", traceResource["service.instance.id"])
	assert.Equal(t, "1.2.3", traceResource["service.version"])
	assert.Equal(t, "test", traceResource["deployment.environment.name"])
	assert.Equal(t, "nomnom", traceResource["service.namespace"])

	require.NotEmpty(t, metrics.requests, "metrics reach telemetry.endpoint")
	metricResource := attributes((<-metrics.requests).GetResourceMetrics()[0].GetResource().GetAttributes())
	assert.Equal(t, "controlplane-blue", metricResource["service.name"])
	assert.Equal(t, "controlplane-0", metricResource["service.instance.id"])

	assert.Empty(t, otherTraces.requests, "OTEL_EXPORTER_OTLP_* is not read")
	assert.Empty(t, otherMetrics.requests, "OTEL_EXPORTER_OTLP_* is not read")
}

// http/protobuf: telemetry.endpoint — базовый адрес, сигналы уходят на /v1/traces и /v1/metrics.
func TestStartExportsOverHTTPProtobuf(t *testing.T) {
	received := make(chan string, 64)
	resources := make(chan map[string]string, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, "application/x-protobuf", r.Header.Get("Content-Type"))
		received <- r.URL.Path
		switch r.URL.Path {
		case "/v1/traces":
			var req coltracepb.ExportTraceServiceRequest
			assert.NoError(t, proto.Unmarshal(body, &req))
			resources <- attributes(req.GetResourceSpans()[0].GetResource().GetAttributes())
		case "/v1/metrics":
			var req colmetricpb.ExportMetricsServiceRequest
			assert.NoError(t, proto.Unmarshal(body, &req))
			resources <- attributes(req.GetResourceMetrics()[0].GetResource().GetAttributes())
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	tel, err := telemetry.Start(context.Background(), telemetryConfig(server.URL, config.TelemetryProtocolHTTPProtobuf, true))
	require.NoError(t, err)
	emitSignals(t, tel)

	close(received)
	close(resources)
	var paths []string
	for path := range received {
		paths = append(paths, path)
	}
	assert.ElementsMatch(t, []string{"/v1/traces", "/v1/metrics"}, paths)
	for resource := range resources {
		assert.Equal(t, "controlplane-blue", resource["service.name"])
		assert.Equal(t, "controlplane-0", resource["service.instance.id"])
	}
}

// AC-1080: endpoint без флагов tracing и metrics ничего не экспортирует.
func TestStartWithoutFlagsExportsNothing(t *testing.T) {
	endpoint, traces, metrics := startGRPCCollector(t)

	tel, err := telemetry.Start(context.Background(), telemetryConfig(endpoint, config.TelemetryProtocolGRPC, false))
	require.NoError(t, err)
	assert.Nil(t, tel.TracerProvider, "traces are not exported")
	emitSignals(t, tel)

	assert.Empty(t, traces.requests)
	assert.Empty(t, metrics.requests)
}

// Runtime- и process-метрики собираются с resource процесса; host-метрики отброшены.
func TestCollectsRuntimeMetricsWithTheProcessResource(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=other,service.version=other")
	res, err := telemetry.Resource(context.Background(), telemetryConfig("", config.TelemetryProtocolGRPC, false).Telemetry)
	require.NoError(t, err)
	reader := sdkmetric.NewManualReader()
	tel, err := telemetry.New(context.Background(), res, func() (sdkmetric.Reader, error) { return reader, nil })
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
	assert.Equal(t, "controlplane-blue", attrs["service.name"])
	assert.Equal(t, "nomnom", attrs["service.namespace"])
	assert.Equal(t, "1.2.3", attrs["service.version"])
	assert.Equal(t, "test", attrs["deployment.environment.name"])
	assert.Equal(t, "controlplane-0", attrs["service.instance.id"])
}
