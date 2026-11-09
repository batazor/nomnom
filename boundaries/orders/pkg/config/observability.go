package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

const (
	// EnvPodName — имя pod (Downward API): service.instance.id; обязателен.
	EnvPodName = "POD_NAME"

	// Протоколы OTLP spec.telemetry.protocol.
	ProtocolGRPC         = "grpc"
	ProtocolHTTPProtobuf = "http/protobuf"

	DefaultTelemetryProtocol     = ProtocolGRPC
	DefaultMetricsExportInterval = 60 * time.Second
	DefaultLogLevel              = "info"

	// Атрибуты resource, которые задаёт сам процесс: resourceAttributes их не переопределяют.
	resourceServiceName       = "service.name"
	resourceServiceInstanceID = "service.instance.id"
)

// ObservabilityConfig — OpenTelemetry и логи процесса (ADR 9): секции
// telemetry, tracing, metrics и logging верхнего уровня spec. Процесс передаёт
// их SDK явно и не читает OTEL_* и LOG__LEVEL.
type ObservabilityConfig struct {
	Telemetry TelemetryConfig `yaml:"telemetry"`
	Tracing   TracingConfig   `yaml:"tracing"`
	Metrics   MetricsConfig   `yaml:"metrics"`
	Logging   LoggingConfig   `yaml:"logging"`
}

// TelemetryConfig — OTLP коллектор и resource процесса.
type TelemetryConfig struct {
	// Endpoint — базовый URL OTLP коллектора (http://alloy:4317); для
	// http/protobuf путь сигнала /v1/traces и /v1/metrics добавляет процесс.
	// Значения по умолчанию нет: обязателен, если включён экспорт.
	Endpoint string `yaml:"endpoint"`
	// Protocol — grpc или http/protobuf.
	Protocol string `yaml:"protocol"`
	// ServiceName — service.name; по умолчанию имя роли процесса.
	ServiceName string `yaml:"serviceName"`
	// ResourceAttributes — атрибуты resource окружения (deployment.environment.name,
	// service.version от chart); service.name и service.instance.id запрещены.
	ResourceAttributes map[string]string `yaml:"resourceAttributes"`
	// InstanceID — service.instance.id из env POD_NAME.
	InstanceID string `yaml:"-"`
}

// TracingConfig включает экспорт spans; trace context передаётся соседям и без него.
type TracingConfig struct {
	Enabled bool `yaml:"enabled"`
}

// MetricsConfig включает экспорт метрик с периодом ExportInterval.
type MetricsConfig struct {
	Enabled        bool          `yaml:"enabled"`
	ExportInterval time.Duration `yaml:"exportInterval"`
}

// LoggingConfig — минимальный уровень JSON-логов: debug, info, warn, error.
type LoggingConfig struct {
	Level string `yaml:"level"`
}

func defaultObservability(serviceName string) ObservabilityConfig {
	return ObservabilityConfig{
		Telemetry: TelemetryConfig{Protocol: DefaultTelemetryProtocol, ServiceName: serviceName},
		Metrics:   MetricsConfig{ExportInterval: DefaultMetricsExportInterval},
		Logging:   LoggingConfig{Level: DefaultLogLevel},
	}
}

// SlogLevel — уровень slog для spec.logging.level; неизвестный уровень — ошибка.
func (cfg LoggingConfig) SlogLevel() (slog.Level, error) {
	switch cfg.Level {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("spec.logging.level must be debug, info, warn or error, got %q", cfg.Level)
	}
}

func (cfg ObservabilityConfig) Validate() error {
	var errs []error
	telemetry := cfg.Telemetry
	switch telemetry.Protocol {
	case ProtocolGRPC, ProtocolHTTPProtobuf:
	default:
		errs = append(errs, fmt.Errorf("spec.telemetry.protocol must be %q or %q, got %q", ProtocolGRPC, ProtocolHTTPProtobuf, telemetry.Protocol))
	}
	if strings.TrimSpace(telemetry.Endpoint) == "" {
		if cfg.Tracing.Enabled || cfg.Metrics.Enabled {
			errs = append(errs, errors.New("spec.telemetry.endpoint is required when spec.tracing.enabled or spec.metrics.enabled is true"))
		}
	} else if endpoint, err := url.Parse(telemetry.Endpoint); err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		errs = append(errs, fmt.Errorf("spec.telemetry.endpoint must be an http or https URL, got %q", telemetry.Endpoint))
	}
	if strings.TrimSpace(telemetry.ServiceName) == "" {
		errs = append(errs, errors.New("spec.telemetry.serviceName must not be empty"))
	}
	for _, key := range []string{resourceServiceName, resourceServiceInstanceID} {
		if _, set := telemetry.ResourceAttributes[key]; set {
			errs = append(errs, fmt.Errorf("spec.telemetry.resourceAttributes must not set %s: the process sets it from spec.telemetry.serviceName and %s", key, EnvPodName))
		}
	}
	if strings.TrimSpace(telemetry.InstanceID) == "" {
		errs = append(errs, fmt.Errorf("%s is required: it is service.instance.id of the process", EnvPodName))
	}
	if cfg.Metrics.ExportInterval <= 0 {
		errs = append(errs, fmt.Errorf("spec.metrics.exportInterval must be positive, got %s", cfg.Metrics.ExportInterval))
	}
	if _, err := cfg.Logging.SlogLevel(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
