// Package telemetry поднимает OpenTelemetry процесса controlplane (ADR 9):
// MeterProvider и TracerProvider с общим resource и OTLP exporters в Alloy.
// Настройки — только из файла конфигурации (spec.telemetry, spec.tracing,
// spec.metrics): endpoint, протокол, resource и интервал передаются SDK явно,
// OTEL_* процесс не читает. Runtime-метрики Go и process.cpu.time собираются
// всегда; host-метрики system.* отбрасываются — это сигналы узла, а не сервиса.
// Отдельный /metrics не открывается: метрики уходят по OTLP.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"

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
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
)

// ServiceNamespace — service.namespace всех процессов (docs/observability.md).
const ServiceNamespace = "nomnom"

// Telemetry — metrics и traces pipeline процесса.
type Telemetry struct {
	MeterProvider *sdkmetric.MeterProvider
	// TracerProvider задаёт Start при tracing.enabled; nil — traces не экспортируются.
	TracerProvider *sdktrace.TracerProvider
}

// ReaderFactory создаёт metric reader; в тестах — ManualReader.
type ReaderFactory func() (sdkmetric.Reader, error)

// Start собирает pipeline и регистрирует его глобальными провайдерами. Spans
// экспортируются только при tracing.enabled, метрики — только при
// metrics.enabled; без флага метрики записываются, но не экспортируются.
func Start(ctx context.Context, cfg config.Config) (*Telemetry, error) {
	res, err := Resource(ctx, cfg.Telemetry)
	if err != nil {
		return nil, err
	}
	var newReader ReaderFactory
	if cfg.Metrics.Enabled {
		newReader = func() (sdkmetric.Reader, error) {
			exporter, err := newMetricExporter(ctx, cfg.Telemetry)
			if err != nil {
				return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
			}
			// Явный интервал имеет приоритет над OTEL_METRIC_EXPORT_INTERVAL.
			return sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(cfg.Metrics.ExportInterval)), nil
		}
	}
	t, err := New(ctx, res, newReader)
	if err != nil {
		return nil, err
	}
	otel.SetMeterProvider(t.MeterProvider)
	// W3C Trace Context и Baggage.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if cfg.Tracing.Enabled {
		exporter, err := newTraceExporter(ctx, cfg.Telemetry)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("create OTLP trace exporter: %w", err), t.MeterProvider.Shutdown(ctx))
		}
		t.TracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
			// Явный sampler имеет приоритет над OTEL_TRACES_SAMPLER.
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		)
		otel.SetTracerProvider(t.TracerProvider)
	}
	return t, nil
}

// Resource — resource всех сигналов процесса: service.namespace, service.version
// (revision сборки), атрибуты telemetry.resourceAttributes (Helm задаёт
// service.version из image tag), service.name и service.instance.id. Env
// OTEL_RESOURCE_ATTRIBUTES и OTEL_SERVICE_NAME не читаются.
func Resource(ctx context.Context, cfg config.TelemetryConfig) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{semconv.ServiceNamespace(ServiceNamespace), semconv.ServiceVersion(buildVersion())}
	for _, key := range slices.Sorted(maps.Keys(cfg.ResourceAttributes)) {
		attrs = append(attrs, attribute.String(key, cfg.ResourceAttributes[key]))
	}
	// Последнее значение ключа побеждает: идентичность процесса не переопределяется.
	attrs = append(attrs, semconv.ServiceName(cfg.ServiceName), semconv.ServiceInstanceID(cfg.InstanceID))
	res, err := resource.New(ctx, resource.WithAttributes(attrs...), resource.WithTelemetrySDK())
	if err != nil {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}
	return res, nil
}

// newTraceExporter — OTLP exporter spans по протоколу из конфигурации. Явный
// endpoint имеет приоритет над OTEL_EXPORTER_OTLP_*.
func newTraceExporter(ctx context.Context, cfg config.TelemetryConfig) (sdktrace.SpanExporter, error) {
	switch cfg.Protocol {
	case config.TelemetryProtocolGRPC:
		return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(cfg.Endpoint))
	case config.TelemetryProtocolHTTPProtobuf:
		endpoint, err := signalURL(cfg.Endpoint, "traces")
		if err != nil {
			return nil, err
		}
		return otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	default:
		return nil, fmt.Errorf("unknown OTLP protocol %q", cfg.Protocol)
	}
}

func newMetricExporter(ctx context.Context, cfg config.TelemetryConfig) (sdkmetric.Exporter, error) {
	switch cfg.Protocol {
	case config.TelemetryProtocolGRPC:
		return otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(cfg.Endpoint))
	case config.TelemetryProtocolHTTPProtobuf:
		endpoint, err := signalURL(cfg.Endpoint, "metrics")
		if err != nil {
			return nil, err
		}
		return otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint))
	default:
		return nil, fmt.Errorf("unknown OTLP protocol %q", cfg.Protocol)
	}
}

// signalURL — URL сигнала OTLP/HTTP: telemetry.endpoint — базовый адрес
// коллектора, к нему добавляется /v1/<signal>, как к OTEL_EXPORTER_OTLP_ENDPOINT.
func signalURL(endpoint, signal string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse OTLP endpoint: %w", err)
	}
	return base.JoinPath("v1", signal).String(), nil
}

// ExponentialHistogram — агрегация всех histograms процесса (ADR 9): base2
// exponential, в Grafana Cloud — native histogram. MaxSize 40 ограничивает
// billable series histogram десятью (0,25 за непустой бакет); MaxScale — значение SDK.
var ExponentialHistogram = sdkmetric.AggregationBase2ExponentialHistogram{MaxSize: 40, MaxScale: 20}

// New собирает metrics pipeline без регистрации глобального провайдера; newReader nil — без экспорта.
func New(ctx context.Context, res *resource.Resource, newReader ReaderFactory) (*Telemetry, error) {
	options := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Name: "system.*"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}})),
		sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram}, sdkmetric.Stream{Aggregation: ExponentialHistogram})),
	}
	if newReader != nil {
		reader, err := newReader()
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
	return &Telemetry{MeterProvider: provider}, nil
}

func (t *Telemetry) Meter(name string) metric.Meter { return t.MeterProvider.Meter(name) }

// Shutdown выполняет финальный export; вызывается после drain, до выхода процесса
// (docs/cloud-native-runtime.md#observability-lifecycle).
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	if t.TracerProvider != nil {
		if err := t.TracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown tracer provider: %w", err))
		}
	}
	if err := t.MeterProvider.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("shutdown meter provider: %w", err))
	}
	return errors.Join(errs...)
}
