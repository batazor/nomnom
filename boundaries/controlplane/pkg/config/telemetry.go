package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"
)

// Протоколы OTLP exporter.
const (
	TelemetryProtocolGRPC         = "grpc"
	TelemetryProtocolHTTPProtobuf = "http/protobuf"
)

const (
	DefaultTelemetryProtocol = TelemetryProtocolGRPC
	// DefaultMetricsExportInterval — как у OpenTelemetry SDK.
	DefaultMetricsExportInterval = 60 * time.Second
	DefaultLogLevel              = "info"
)

// Атрибуты resource, которыми владеет процесс: service.name задаёт
// telemetry.serviceName, service.instance.id — env POD_NAME.
const (
	resourceAttributeServiceName       = "service.name"
	resourceAttributeServiceInstanceID = "service.instance.id"
)

// TelemetryConfig — OpenTelemetry процесса (ADR 9). Процесс передаёт SDK endpoint,
// протокол и resource явно и не читает OTEL_*.
type TelemetryConfig struct {
	// Endpoint — URL OTLP коллектора; значения по умолчанию нет, обязателен при
	// включённом tracing или metrics.
	Endpoint string `yaml:"endpoint"`
	// Protocol — grpc или http/protobuf.
	Protocol string `yaml:"protocol"`
	// ServiceName — service.name; по умолчанию имя процесса.
	ServiceName string `yaml:"serviceName"`
	// ResourceAttributes — остальные атрибуты resource (deployment.environment.name,
	// service.version из image tag); service.name и service.instance.id не задаются.
	ResourceAttributes map[string]string `yaml:"resourceAttributes"`
	// InstanceID — service.instance.id из env POD_NAME; в файле не задаётся.
	InstanceID string `yaml:"-"`
}

// TracingConfig включает экспорт spans; endpoint без флага экспорт не включает.
type TracingConfig struct {
	Enabled bool `yaml:"enabled"`
}

// MetricsConfig включает экспорт метрик; без флага метрики записываются, но не экспортируются.
type MetricsConfig struct {
	Enabled        bool          `yaml:"enabled"`
	ExportInterval time.Duration `yaml:"exportInterval"`
}

// LoggingConfig — минимальный уровень JSON-логов процесса.
type LoggingConfig struct {
	Level string `yaml:"level"`
}

func (cfg TelemetryConfig) Validate(tracing TracingConfig, metrics MetricsConfig) error {
	switch cfg.Protocol {
	case TelemetryProtocolGRPC, TelemetryProtocolHTTPProtobuf:
	default:
		return fmt.Errorf("spec.telemetry.protocol must be %s or %s, got %q", TelemetryProtocolGRPC, TelemetryProtocolHTTPProtobuf, cfg.Protocol)
	}
	if cfg.ServiceName == "" {
		return errors.New("spec.telemetry.serviceName must not be empty")
	}
	for _, key := range []string{resourceAttributeServiceName, resourceAttributeServiceInstanceID} {
		if _, ok := cfg.ResourceAttributes[key]; ok {
			return fmt.Errorf("spec.telemetry.resourceAttributes must not contain %s: the process sets it", key)
		}
	}
	if cfg.InstanceID == "" {
		return fmt.Errorf("%s is required: service.instance.id of the process telemetry", EnvPodName)
	}
	if metrics.ExportInterval <= 0 {
		return fmt.Errorf("spec.metrics.exportInterval must be positive, got %s", metrics.ExportInterval)
	}
	if cfg.Endpoint == "" {
		if tracing.Enabled || metrics.Enabled {
			return errors.New("spec.telemetry.endpoint is required when spec.tracing.enabled or spec.metrics.enabled is true")
		}
		return nil
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("spec.telemetry.endpoint: %w", err)
	}
	if (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return fmt.Errorf("spec.telemetry.endpoint must be an http or https URL with a host, got %q", cfg.Endpoint)
	}
	return nil
}

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

func (cfg LoggingConfig) Validate() error {
	if _, ok := logLevels[cfg.Level]; !ok {
		return fmt.Errorf("spec.logging.level must be debug, info, warn or error, got %q", cfg.Level)
	}
	return nil
}

// SlogLevel — уровень проверенной конфигурации.
func (cfg LoggingConfig) SlogLevel() slog.Level {
	return logLevels[cfg.Level]
}
