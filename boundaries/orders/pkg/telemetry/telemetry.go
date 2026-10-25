// Package telemetry поднимает OpenTelemetry metrics процесса orders (ADR 9):
// MeterProvider с общим resource, OTLP exporter в Alloy и Prometheus bridge для
// библиотек, которые пишут только в prometheus.Registerer (Watermill
// components/metrics). Один pipeline: метрики bridge уходят тем же OTLP с теми же
// resource attributes, отдельный /metrics не открывается.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/prometheus/client_golang/prometheus"
)

// ServiceNamespace — service.namespace всех процессов (docs/observability.md).
const ServiceNamespace = "nomnom"

// Metrics — metrics pipeline процесса. Registry принимает метрики библиотек на
// Prometheus client; MeterProvider собирает их через bridge вместе с OTel instruments.
type Metrics struct {
	Registry      *prometheus.Registry
	MeterProvider *sdkmetric.MeterProvider
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
	return m, nil
}

// New собирает pipeline без регистрации глобального провайдера; newReader nil — без экспорта.
func New(ctx context.Context, serviceName string, newReader ReaderFactory) (*Metrics, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName), semconv.ServiceNamespace(ServiceNamespace)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}

	registry := prometheus.NewRegistry()
	options := []sdkmetric.Option{sdkmetric.WithResource(res)}
	if newReader != nil {
		reader, err := newReader(prombridge.NewMetricProducer(prombridge.WithGatherer(registry)))
		if err != nil {
			return nil, err
		}
		options = append(options, sdkmetric.WithReader(reader))
	}
	return &Metrics{Registry: registry, MeterProvider: sdkmetric.NewMeterProvider(options...)}, nil
}

func (m *Metrics) Meter(name string) metric.Meter { return m.MeterProvider.Meter(name) }

// Shutdown выполняет финальный export; вызывается после drain, до выхода процесса
// (docs/cloud-native-runtime.md#observability-lifecycle).
func (m *Metrics) Shutdown(ctx context.Context) error {
	if err := m.MeterProvider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown meter provider: %w", err)
	}
	return nil
}

func endpointConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}
