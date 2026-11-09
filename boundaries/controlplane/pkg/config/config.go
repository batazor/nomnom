// Package config loads the configuration of the controlplane process from the
// versioned file at CONFIG_FILE (apiVersion config.nomnom.dev/v1alpha1, kind
// ControlPlane). Non-secret settings come only from the file: SECTION__KEY
// environment variables are not read. The process has no secrets. A missing
// file, a foreign apiVersion, kind or role, an unknown key and a required
// setting without a value stop the start: the process never runs with part of
// its operations off.
package config

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v4"
)

const (
	// APIVersion — версия формата файла; несовместимое изменение spec объявляется новой версией.
	APIVersion = "config.nomnom.dev/v1alpha1"
	// Kind — сервис: все роли controlplane делят один kind.
	Kind = "ControlPlane"

	// EnvConfigFile — путь к файлу конфигурации; обязателен.
	EnvConfigFile = "CONFIG_FILE"
	// EnvPodName — имя pod (Downward API), service.instance.id telemetry; обязателен.
	EnvPodName = "POD_NAME"
)

// Config — spec файла конфигурации процесса.
type Config struct {
	Management ManagementConfig `yaml:"management"`
	Shutdown   ShutdownConfig   `yaml:"shutdown"`
	Routes     RoutesConfig     `yaml:"routes"`
	Telemetry  TelemetryConfig  `yaml:"telemetry"`
	Tracing    TracingConfig    `yaml:"tracing"`
	Metrics    MetricsConfig    `yaml:"metrics"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// document — файл конфигурации в виде ресурса Kubernetes. В Kubernetes API он не
// регистрируется: это формат файла, а не CRD.
type document struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   metadata `yaml:"metadata"`
	Spec       Config   `yaml:"spec"`
}

type metadata struct {
	// Name — роль процесса: файл чужой роли не принимается молча.
	Name string `yaml:"name"`
}

// Load читает файл CONFIG_FILE процесса роли role. serviceName — service.name
// telemetry по умолчанию, если spec.telemetry.serviceName не задан.
func Load(role, serviceName string) (Config, error) {
	path := os.Getenv(EnvConfigFile)
	if path == "" {
		return Config{}, fmt.Errorf("%s is required: the path to the configuration file of the process", EnvConfigFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg, err := parse(data, role, serviceName)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.Telemetry.InstanceID = os.Getenv(EnvPodName)
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// parse проверяет apiVersion, kind и роль, затем строго декодирует spec поверх
// значений по умолчанию.
func parse(data []byte, role, serviceName string) (Config, error) {
	// Load требует ровно один документ: пустой файл и поток документов отклоняются.
	var root yaml.Node
	if err := yaml.Load(data, &root); err != nil {
		return Config{}, fmt.Errorf("parse: %w", err)
	}

	// Заголовок сверяется до spec: spec чужого kind дал бы ошибки о чужих ключах.
	var header struct {
		APIVersion string   `yaml:"apiVersion"`
		Kind       string   `yaml:"kind"`
		Metadata   metadata `yaml:"metadata"`
	}
	if err := root.Load(&header); err != nil {
		return Config{}, fmt.Errorf("decode header: %w", err)
	}
	if header.APIVersion != APIVersion || header.Kind != Kind {
		return Config{}, fmt.Errorf("apiVersion and kind must be %s %s, got %q %q", APIVersion, Kind, header.APIVersion, header.Kind)
	}
	if header.Metadata.Name != role {
		return Config{}, fmt.Errorf("metadata.name must be the role of the process %q, got %q", role, header.Metadata.Name)
	}

	if err := checkKnownFields(root.Content[0], documentType, ""); err != nil {
		return Config{}, err
	}

	doc := document{Spec: defaults(serviceName)}
	if err := yaml.Load(data, &doc, yaml.WithKnownFields()); err != nil {
		return Config{}, fmt.Errorf("decode: %w", err)
	}
	return doc.Spec, nil
}

// defaults — явные значения по умолчанию; настройки без них обязательны.
func defaults(serviceName string) Config {
	return Config{
		Management: ManagementConfig{Port: DefaultManagementPort},
		Shutdown:   ShutdownConfig{ReadinessDelay: DefaultReadinessDelay, DrainTimeout: DefaultDrainTimeout},
		Telemetry:  TelemetryConfig{Protocol: DefaultTelemetryProtocol, ServiceName: serviceName},
		Metrics:    MetricsConfig{ExportInterval: DefaultMetricsExportInterval},
		Logging:    LoggingConfig{Level: DefaultLogLevel},
	}
}

// Validate checks every section; the first invalid one stops the start.
func (cfg Config) Validate() error {
	for _, validate := range []func() error{
		cfg.Management.Validate, cfg.Shutdown.Validate, cfg.Routes.Validate, cfg.Logging.Validate,
		func() error { return cfg.Telemetry.Validate(cfg.Tracing, cfg.Metrics) },
	} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}
