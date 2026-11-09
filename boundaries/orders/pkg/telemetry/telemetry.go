// Package telemetry поднимает OpenTelemetry процесса orders (ADR 9) из секций
// spec.telemetry, spec.tracing, spec.metrics и spec.logging файла конфигурации:
// MeterProvider с общим resource, OTLP exporter (grpc или http/protobuf) в Alloy и Prometheus bridge для
// библиотек, которые пишут только в prometheus.Registerer (Watermill
// components/metrics). Runtime-метрики Go и process.cpu.time собираются для
// каждого процесса; host-метрики system.* отбрасываются — это сигналы узла, а не сервиса. Один pipeline: метрики bridge уходят тем же OTLP с теми же
// resource attributes, отдельный /metrics не открывается.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/prometheus/client_golang/prometheus"
	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"

	"nomnom/orders/pkg/config"
)

// ServiceNamespace — service.namespace всех процессов (docs/observability.md).
const ServiceNamespace = "nomnom"

// TemporalMeterName — scope метрик Temporal SDK (contrib/opentelemetry).
const TemporalMeterName = "temporal-sdk-go"

// PgxScope — instrumentation scope otelpgx (traces и db.client.operation.duration).
const PgxScope = "github.com/exaring/otelpgx"

// DBOperationDurationMetric — semconv histogram длительности операций БД.
const DBOperationDurationMetric = "db.client.operation.duration"

// DBOperationDurationAttributes — labels db_client_operation_duration_seconds
// из ops/grafana/inventory; pgx.operation.type otelpgx отбрасывается.
var DBOperationDurationAttributes = []attribute.Key{
	semconv.DBSystemNameKey, semconv.DBOperationNameKey, semconv.DBCollectionNameKey, semconv.ErrorTypeKey,
}

// SecondsBuckets — границы histograms длительностей в секундах.
var SecondsBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300}

// Metrics — metrics pipeline процесса. Registry принимает метрики библиотек на
// Prometheus client; MeterProvider собирает их через bridge вместе с OTel instruments.
type Metrics struct {
	Registry      *prometheus.Registry
	MeterProvider *sdkmetric.MeterProvider
	// TracerProvider задаёт Start при tracing.enabled; nil — traces не экспортируются.
	TracerProvider *sdktrace.TracerProvider
}

// ReaderFactory создаёт reader поверх producer bridge; в тестах — ManualReader.
type ReaderFactory func(producer sdkmetric.Producer) (sdkmetric.Reader, error)

// Start собирает pipeline из spec.telemetry, spec.tracing и spec.metrics и
// регистрирует глобальные провайдеры. Endpoint, протокол, resource и интервал
// передаются SDK явно, OTEL_* процесс не читает. Метрики экспортируются только
// при metrics.enabled, spans — только при tracing.enabled; без флагов метрики
// записываются, но не экспортируются, а trace context всё равно передаётся.
func Start(ctx context.Context, cfg config.ObservabilityConfig) (*Metrics, error) {
	var newReader ReaderFactory
	if cfg.Metrics.Enabled {
		newReader = func(producer sdkmetric.Producer) (sdkmetric.Reader, error) {
			exporter, err := newMetricExporter(ctx, cfg.Telemetry)
			if err != nil {
				return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
			}
			// Явный интервал имеет приоритет над OTEL_METRIC_EXPORT_INTERVAL, который SDK читает сам.
			return sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithProducer(producer), sdkmetric.WithInterval(cfg.Metrics.ExportInterval)), nil
		}
	}
	m, err := New(ctx, cfg.Telemetry, newReader)
	if err != nil {
		return nil, err
	}
	otel.SetMeterProvider(m.MeterProvider)
	// W3C Trace Context и Baggage: otelgrpc продолжает trace вызывающего из traceparent
	// и передаёт его соседям; без propagator контекст теряется на каждом hop.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if cfg.Tracing.Enabled {
		exporter, err := newTraceExporter(ctx, cfg.Telemetry)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("create OTLP trace exporter: %w", err), m.MeterProvider.Shutdown(ctx))
		}
		res, err := newResource(ctx, cfg.Telemetry)
		if err != nil {
			return nil, errors.Join(err, m.MeterProvider.Shutdown(ctx))
		}
		m.TracerProvider = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
		otel.SetTracerProvider(m.TracerProvider)
	}
	return m, nil
}

// newTraceExporter и newMetricExporter получают endpoint явно: опция имеет
// приоритет над OTEL_EXPORTER_OTLP_*, которые exporters SDK читают сами.
// Схема http endpoint — соединение без TLS.
func newTraceExporter(ctx context.Context, cfg config.TelemetryConfig) (sdktrace.SpanExporter, error) {
	switch cfg.Protocol {
	case config.ProtocolGRPC:
		return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(cfg.Endpoint))
	case config.ProtocolHTTPProtobuf:
		return otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(signalURL(cfg.Endpoint, "traces")))
	default:
		return nil, fmt.Errorf("unknown OTLP protocol %q", cfg.Protocol)
	}
}

func newMetricExporter(ctx context.Context, cfg config.TelemetryConfig) (sdkmetric.Exporter, error) {
	switch cfg.Protocol {
	case config.ProtocolGRPC:
		return otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(cfg.Endpoint))
	case config.ProtocolHTTPProtobuf:
		return otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(signalURL(cfg.Endpoint, "metrics")))
	default:
		return nil, fmt.Errorf("unknown OTLP protocol %q", cfg.Protocol)
	}
}

// signalURL — URL сигнала OTLP/HTTP: базовый endpoint коллектора и /v1/<signal>,
// как у OTEL_EXPORTER_OTLP_ENDPOINT.
func signalURL(endpoint, signal string) string {
	return strings.TrimRight(endpoint, "/") + "/v1/" + signal
}

// newResource — resource всех сигналов: service.namespace, service.version
// (revision сборки; resourceAttributes, например service.version от chart,
// её переопределяют), атрибуты окружения, service.name из serviceName и
// service.instance.id из POD_NAME. resource.Environment() SDK подмешивает сам
// (WithResource), но эти атрибуты имеют приоритет.
func newResource(ctx context.Context, cfg config.TelemetryConfig) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{semconv.ServiceNamespace(ServiceNamespace), semconv.ServiceVersion(buildVersion())}
	for _, key := range slices.Sorted(maps.Keys(cfg.ResourceAttributes)) {
		attrs = append(attrs, attribute.String(key, cfg.ResourceAttributes[key]))
	}
	attrs = append(attrs, semconv.ServiceName(cfg.ServiceName))
	if cfg.InstanceID != "" {
		attrs = append(attrs, semconv.ServiceInstanceID(cfg.InstanceID))
	}
	res, err := resource.New(ctx, resource.WithAttributes(attrs...), resource.WithTelemetrySDK())
	if err != nil {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}
	return res, nil
}

// New собирает pipeline без регистрации глобального провайдера; newReader nil — без экспорта.
func New(ctx context.Context, cfg config.TelemetryConfig, newReader ReaderFactory) (*Metrics, error) {
	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	registry := prometheus.NewRegistry()
	options := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Name: "system.*"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}})),
		// otelpgx: db.client.operation.duration — только с attributes инвентаря
		// (section db); собственный counter db.client.operation.errors не входит
		// в semconv и в ops/semconv, поэтому отбрасывается.
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: DBOperationDurationMetric, Scope: instrumentation.Scope{Name: PgxScope}},
			sdkmetric.Stream{AttributeFilter: attribute.NewAllowKeysFilter(DBOperationDurationAttributes...)},
		)),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "db.client.operation.errors", Scope: instrumentation.Scope{Name: PgxScope}},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}},
		)),
		// Timers Temporal SDK — histograms в секундах без своих границ; дефолтные
		// границы SDK рассчитаны на миллисекунды.
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram, Scope: instrumentation.Scope{Name: TemporalMeterName}},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: SecondsBuckets}},
		)),
	}
	if newReader != nil {
		reader, err := newReader(prombridge.NewMetricProducer(prombridge.WithGatherer(registry)))
		if err != nil {
			return nil, err
		}
		options = append(options, sdkmetric.WithReader(reader))
	}
	provider := sdkmetric.NewMeterProvider(options...)
	if err := runtime.Start(runtime.WithMeterProvider(provider)); err != nil {
		return nil, errors.Join(fmt.Errorf("start runtime metrics: %w", err), provider.Shutdown(ctx))
	}
	if err := host.Start(host.WithMeterProvider(provider)); err != nil {
		return nil, errors.Join(fmt.Errorf("start process metrics: %w", err), provider.Shutdown(ctx))
	}
	return &Metrics{Registry: registry, MeterProvider: provider}, nil
}

func (m *Metrics) Meter(name string) metric.Meter { return m.MeterProvider.Meter(name) }

// TemporalMetricsHandler — metrics Temporal SDK в общий pipeline. Counters
// монотонные: иначе SDK пишет UpDownCounter, backend получает gauge без _total, и
// rate() по нему некорректен.
func (m *Metrics) TemporalMetricsHandler() client.MetricsHandler {
	return temporalotel.NewMetricsHandler(temporalotel.MetricsHandlerOptions{
		Meter:                m.Meter(TemporalMeterName),
		UseMonotonicCounters: true,
	})
}

// Shutdown выполняет финальный export; вызывается после drain, до выхода процесса
// (docs/cloud-native-runtime.md#observability-lifecycle).
func (m *Metrics) Shutdown(ctx context.Context) error {
	var errs []error
	if m.TracerProvider != nil {
		if err := m.TracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown tracer provider: %w", err))
		}
	}
	if err := m.MeterProvider.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("shutdown meter provider: %w", err))
	}
	return errors.Join(errs...)
}
