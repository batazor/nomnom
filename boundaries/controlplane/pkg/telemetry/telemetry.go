// Package telemetry поднимает OpenTelemetry процесса controlplane (ADR 9):
// MeterProvider и TracerProvider с общим resource и OTLP exporters в Alloy.
// Runtime-метрики Go и process.cpu.time собираются всегда; host-метрики system.*
// отбрасываются — это сигналы узла, а не сервиса. Отдельный /metrics не
// открывается: метрики уходят по OTLP.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

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
)

// ServiceNamespace — service.namespace всех процессов (docs/observability.md).
const ServiceNamespace = "nomnom"

// PgxScope — instrumentation scope otelpgx (traces и db.client.operation.duration).
const PgxScope = "github.com/exaring/otelpgx"

// DBOperationDurationMetric — semconv histogram длительности операций БД.
const DBOperationDurationMetric = "db.client.operation.duration"

// DBOperationDurationAttributes — labels db_client_operation_duration_seconds
// из ops/grafana/inventory; pgx.operation.type otelpgx отбрасывается.
var DBOperationDurationAttributes = []attribute.Key{
	semconv.DBSystemNameKey, semconv.DBOperationNameKey, semconv.DBCollectionNameKey, semconv.ErrorTypeKey,
}

// Telemetry — metrics и traces pipeline процесса.
type Telemetry struct {
	MeterProvider *sdkmetric.MeterProvider
	// TracerProvider задаёт Start при настроенном OTLP traces endpoint; nil — traces не экспортируются.
	TracerProvider *sdktrace.TracerProvider
}

// ReaderFactory создаёт metric reader; в тестах — ManualReader.
type ReaderFactory func() (sdkmetric.Reader, error)

// Start собирает pipeline и регистрирует его глобальными провайдерами.
// service.name по умолчанию — serviceName; OTEL_SERVICE_NAME и
// OTEL_RESOURCE_ATTRIBUTES его переопределяют. Без OTEL_EXPORTER_OTLP_ENDPOINT
// (или OTEL_EXPORTER_OTLP_METRICS_ENDPOINT) метрики записываются, но не экспортируются.
func Start(ctx context.Context, serviceName string) (*Telemetry, error) {
	var newReader ReaderFactory
	if endpointConfigured() {
		newReader = func() (sdkmetric.Reader, error) {
			exporter, err := otlpmetricgrpc.New(ctx)
			if err != nil {
				return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
			}
			return sdkmetric.NewPeriodicReader(exporter), nil
		}
	}
	t, err := New(ctx, serviceName, newReader)
	if err != nil {
		return nil, err
	}
	otel.SetMeterProvider(t.MeterProvider)
	// W3C Trace Context и Baggage: otelgrpc продолжает trace вызывающего из traceparent.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if tracesEndpointConfigured() {
		exporter, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("create OTLP trace exporter: %w", err), t.MeterProvider.Shutdown(ctx))
		}
		res, err := newResource(ctx, serviceName)
		if err != nil {
			return nil, errors.Join(err, t.MeterProvider.Shutdown(ctx))
		}
		t.TracerProvider = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
		otel.SetTracerProvider(t.TracerProvider)
	}
	return t, nil
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

// New собирает metrics pipeline без регистрации глобального провайдера; newReader nil — без экспорта.
func New(ctx context.Context, serviceName string, newReader ReaderFactory) (*Telemetry, error) {
	res, err := newResource(ctx, serviceName)
	if err != nil {
		return nil, err
	}
	options := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Name: "system.*"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}})),
		// otelpgx: db.client.operation.duration — только с attributes инвентаря;
		// собственный counter db.client.operation.errors не входит в semconv.
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: DBOperationDurationMetric, Scope: instrumentation.Scope{Name: PgxScope}},
			sdkmetric.Stream{AttributeFilter: attribute.NewAllowKeysFilter(DBOperationDurationAttributes...)},
		)),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "db.client.operation.errors", Scope: instrumentation.Scope{Name: PgxScope}},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}},
		)),
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

func tracesEndpointConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}

func endpointConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}
