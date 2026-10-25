// Package telemetry поднимает OpenTelemetry metrics процесса orders (ADR 9):
// MeterProvider с общим resource, OTLP exporter в Alloy и Prometheus bridge для
// библиотек, которые пишут только в prometheus.Registerer (Watermill
// components/metrics). Runtime-метрики Go и process.cpu.time собираются для
// каждого процесса; host-метрики system.* отбрасываются — это сигналы узла, а не сервиса. Один pipeline: метрики bridge уходят тем же OTLP с теми же
// resource attributes, отдельный /metrics не открывается.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
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
	// TracerProvider задаёт Start при настроенном OTLP traces endpoint; nil — traces не экспортируются.
	TracerProvider *sdktrace.TracerProvider
}

// ReaderFactory создаёт reader поверх producer bridge; в тестах — ManualReader.
type ReaderFactory func(producer sdkmetric.Producer) (sdkmetric.Reader, error)

// Start собирает pipeline и регистрирует его глобальным MeterProvider.
// service.name по умолчанию — serviceName; OTEL_SERVICE_NAME и
// OTEL_RESOURCE_ATTRIBUTES его переопределяют. Без OTEL_EXPORTER_OTLP_ENDPOINT
// (или OTEL_EXPORTER_OTLP_METRICS_ENDPOINT) метрики записываются, но не экспортируются.
func Start(ctx context.Context, serviceName string) (*Metrics, error) {
	var newReader ReaderFactory
	if endpointConfigured() {
		newReader = func(producer sdkmetric.Producer) (sdkmetric.Reader, error) {
			exporter, err := otlpmetricgrpc.New(ctx)
			if err != nil {
				return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
			}
			return sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithProducer(producer)), nil
		}
	}
	m, err := New(ctx, serviceName, newReader)
	if err != nil {
		return nil, err
	}
	otel.SetMeterProvider(m.MeterProvider)
	// W3C Trace Context и Baggage: otelgrpc продолжает trace вызывающего из traceparent
	// и передаёт его соседям; без propagator контекст теряется на каждом hop.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if tracesEndpointConfigured() {
		exporter, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("create OTLP trace exporter: %w", err), m.MeterProvider.Shutdown(ctx))
		}
		res, err := newResource(ctx, serviceName)
		if err != nil {
			return nil, errors.Join(err, m.MeterProvider.Shutdown(ctx))
		}
		m.TracerProvider = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
		otel.SetTracerProvider(m.TracerProvider)
	}
	return m, nil
}

func newResource(ctx context.Context, serviceName string) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		// service.version по умолчанию — revision сборки; OTEL_RESOURCE_ATTRIBUTES
		// (Helm задаёт image tag) переопределяет. Одно значение для всех сигналов.
		resource.WithAttributes(semconv.ServiceName(serviceName), semconv.ServiceNamespace(ServiceNamespace), semconv.ServiceVersion(buildVersion())),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}
	return res, nil
}

// New собирает pipeline без регистрации глобального провайдера; newReader nil — без экспорта.
func New(ctx context.Context, serviceName string, newReader ReaderFactory) (*Metrics, error) {
	res, err := newResource(ctx, serviceName)
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
// монотонные: иначе SDK пишет UpDownCounter, Mimir получает gauge без _total, и
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

func tracesEndpointConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}

func endpointConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}
