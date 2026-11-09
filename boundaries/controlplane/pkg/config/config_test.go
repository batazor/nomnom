package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRole        = "controlplane"
	testServiceName = "controlplane"
)

// header — заголовок файла роли controlplane.
const header = `apiVersion: config.nomnom.dev/v1alpha1
kind: ControlPlane
metadata:
  name: controlplane
`

// minimalFile — минимальный валидный файл: обязательна только секция routes.
const minimalFile = header + `spec:
  routes:
    namespace: nomnom-gateway
    gateway: public
    listener: api
`

// useConfigFile пишет файл конфигурации и задаёт CONFIG_FILE и POD_NAME процесса.
func useConfigFile(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	t.Setenv(EnvConfigFile, path)
	t.Setenv(EnvPodName, "controlplane-0")
}

// AC-1067: файл своей роли принимается; значения по умолчанию — явные DEFAULT_*.
func TestLoadReadsTheFileOfTheRole(t *testing.T) {
	useConfigFile(t, minimalFile)

	cfg, err := Load(testRole, testServiceName)
	require.NoError(t, err)
	assert.Equal(t, Config{
		Management: ManagementConfig{Port: DefaultManagementPort},
		Shutdown:   ShutdownConfig{ReadinessDelay: DefaultReadinessDelay, DrainTimeout: DefaultDrainTimeout},
		Routes:     RoutesConfig{Namespace: "nomnom-gateway", Gateway: "public", Listener: "api"},
		Telemetry:  TelemetryConfig{Protocol: TelemetryProtocolGRPC, ServiceName: testServiceName, InstanceID: "controlplane-0"},
		Metrics:    MetricsConfig{ExportInterval: 60 * time.Second},
		Logging:    LoggingConfig{Level: "info"},
	}, cfg)
	assert.Equal(t, slog.LevelInfo, cfg.Logging.SlogLevel())
}

func TestLoadReadsEverySectionOfTheFile(t *testing.T) {
	useConfigFile(t, header+`spec:
  management:
    port: 9000
  shutdown:
    readinessDelay: 1s
    drainTimeout: 500ms
  routes:
    namespace: nomnom-gateway
    gateway: public
    listener: api
  telemetry:
    endpoint: http://alloy.nomnom-observability.svc.cluster.local:4318
    protocol: http/protobuf
    serviceName: controlplane-blue
    resourceAttributes:
      deployment.environment.name: kind
      service.version: 1.2.3
  tracing:
    enabled: true
  metrics:
    enabled: true
    exportInterval: 15s
  logging:
    level: debug
`)

	cfg, err := Load(testRole, testServiceName)
	require.NoError(t, err)
	assert.Equal(t, Config{
		Management: ManagementConfig{Port: 9000},
		Shutdown:   ShutdownConfig{ReadinessDelay: time.Second, DrainTimeout: 500 * time.Millisecond},
		Routes:     RoutesConfig{Namespace: "nomnom-gateway", Gateway: "public", Listener: "api"},
		Telemetry: TelemetryConfig{
			Endpoint:           "http://alloy.nomnom-observability.svc.cluster.local:4318",
			Protocol:           TelemetryProtocolHTTPProtobuf,
			ServiceName:        "controlplane-blue",
			ResourceAttributes: map[string]string{"deployment.environment.name": "kind", "service.version": "1.2.3"},
			InstanceID:         "controlplane-0",
		},
		Tracing: TracingConfig{Enabled: true},
		Metrics: MetricsConfig{Enabled: true, ExportInterval: 15 * time.Second},
		Logging: LoggingConfig{Level: "debug"},
	}, cfg)
	assert.Equal(t, slog.LevelDebug, cfg.Logging.SlogLevel())
}

// config.example.yaml (make run) — валидный файл роли.
func TestLoadAcceptsTheExampleFile(t *testing.T) {
	t.Setenv(EnvConfigFile, filepath.Join("..", "..", "config.example.yaml"))
	t.Setenv(EnvPodName, "controlplane-local")

	_, err := Load(testRole, testServiceName)
	require.NoError(t, err)
}

// AC-1068: чужие apiVersion или kind — ошибка с ожидаемыми значениями.
func TestLoadRejectsForeignAPIVersionOrKind(t *testing.T) {
	for name, content := range map[string]string{
		"apiVersion": "apiVersion: config.nomnom.dev/v1beta1\nkind: ControlPlane\nmetadata:\n  name: controlplane\nspec: {}\n",
		"kind":       "apiVersion: config.nomnom.dev/v1alpha1\nkind: Delivery\nmetadata:\n  name: controlplane\nspec: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			useConfigFile(t, content)

			_, err := Load(testRole, testServiceName)
			require.ErrorContains(t, err, "config.nomnom.dev/v1alpha1")
			require.ErrorContains(t, err, "ControlPlane")
		})
	}
}

// AC-1069: файл другой роли не принимается.
func TestLoadRejectsTheFileOfAnotherRole(t *testing.T) {
	useConfigFile(t, "apiVersion: config.nomnom.dev/v1alpha1\nkind: ControlPlane\nmetadata:\n  name: controlplane-canary\nspec: {}\n")

	_, err := Load(testRole, testServiceName)
	require.ErrorContains(t, err, "metadata.name")
	require.ErrorContains(t, err, "controlplane-canary")
}

// AC-1071: без CONFIG_FILE или с путём к несуществующему файлу процесс не стартует.
func TestLoadRequiresTheConfigFile(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv(EnvConfigFile, "")
		require.NoError(t, os.Unsetenv(EnvConfigFile))
		t.Setenv(EnvPodName, "controlplane-0")

		_, err := Load(testRole, testServiceName)
		require.ErrorContains(t, err, EnvConfigFile)
	})
	t.Run("missing file", func(t *testing.T) {
		t.Setenv(EnvConfigFile, filepath.Join(t.TempDir(), "absent.yaml"))
		t.Setenv(EnvPodName, "controlplane-0")

		_, err := Load(testRole, testServiceName)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("empty file", func(t *testing.T) {
		useConfigFile(t, "")

		_, err := Load(testRole, testServiceName)
		require.ErrorContains(t, err, "empty")
	})
}

// AC-1072, AC-1073: неизвестный ключ на любом уровне — ошибка с путём ключа.
func TestLoadRejectsUnknownKeysWithTheirPath(t *testing.T) {
	for path, content := range map[string]string{
		"spec.shutdown.drainTimout": minimalFile + "  shutdown:\n    drainTimout: 20s\n",
		"status":                    minimalFile + "status:\n  ready: true\n",
		"metadata.labels":           "apiVersion: config.nomnom.dev/v1alpha1\nkind: ControlPlane\nmetadata:\n  name: controlplane\n  labels: {}\nspec: {}\n",
		"spec.routes.port":          minimalFile + "    port: 80\n",
		"spec.tracing.endpoint":     minimalFile + "  tracing:\n    endpoint: http://alloy:4317\n",
	} {
		t.Run(path, func(t *testing.T) {
			useConfigFile(t, content)

			_, err := Load(testRole, testServiceName)
			require.ErrorContains(t, err, path+": unknown field")
		})
	}
}

// AC-1014: конфигурации PostgreSQL, SpiceDB и identity у процесса нет — в spec нет и полей секретов.
func TestLoadRejectsSectionsOfTheRemovedAPI(t *testing.T) {
	for _, section := range []string{"postgres", "spicedb", "identity", "server"} {
		t.Run(section, func(t *testing.T) {
			useConfigFile(t, minimalFile+"  "+section+":\n    value: x\n")

			_, err := Load(testRole, testServiceName)
			require.ErrorContains(t, err, "spec."+section+": unknown field")
		})
	}
}

// AC-1074, AC-1079: env SECTION__KEY, LOG__LEVEL и OTEL_* на конфигурацию не влияют.
func TestLoadIgnoresTheEnvironment(t *testing.T) {
	useConfigFile(t, minimalFile+"  shutdown:\n    drainTimeout: 20s\n")
	for env, value := range map[string]string{
		"SHUTDOWN__DRAIN_TIMEOUT":     "0ms",
		"MANAGEMENT__PORT":            "70000",
		"ROUTES__NAMESPACE":           "other",
		"LOG__LEVEL":                  "verbose",
		"OTEL_SERVICE_NAME":           "other",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://other:4317",
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
	} {
		t.Setenv(env, value)
	}

	cfg, err := Load(testRole, testServiceName)
	require.NoError(t, err)
	assert.Equal(t, 20*time.Second, cfg.Shutdown.DrainTimeout)
	assert.Equal(t, DefaultManagementPort, cfg.Management.Port)
	assert.Equal(t, "nomnom-gateway", cfg.Routes.Namespace)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, testServiceName, cfg.Telemetry.ServiceName)
	assert.Empty(t, cfg.Telemetry.Endpoint)
	assert.Equal(t, TelemetryProtocolGRPC, cfg.Telemetry.Protocol)
}

// AC-810: пустой обязательный параметр останавливает старт, а не отключает операции.
func TestLoadRejectsMissingRequiredSettings(t *testing.T) {
	for _, key := range []string{"namespace", "gateway", "listener"} {
		t.Run(key, func(t *testing.T) {
			routes := map[string]string{"namespace": "nomnom-gateway", "gateway": "public", "listener": "api"}
			routes[key] = `""`
			useConfigFile(t, header+"spec:\n  routes:\n    namespace: "+routes["namespace"]+"\n    gateway: "+routes["gateway"]+"\n    listener: "+routes["listener"]+"\n")

			_, err := Load(testRole, testServiceName)
			require.ErrorContains(t, err, "spec.routes."+key+" is required")
		})
	}
}

// Fail fast при старте: невалидное значение в файле останавливает старт.
func TestLoadRejectsInvalidValues(t *testing.T) {
	for want, section := range map[string]string{
		"spec.management.port must be between 1 and 65535":      "  management:\n    port: 70000\n",
		"spec.shutdown.drainTimeout must be positive":           "  shutdown:\n    drainTimeout: 0ms\n",
		"spec.shutdown.readinessDelay must not be negative":     "  shutdown:\n    readinessDelay: -1s\n",
		"spec.shutdown.drainTimeout: duration must be a string": "  shutdown:\n    drainTimeout: 20\n",
		"spec.metrics.exportInterval must be positive":          "  metrics:\n    exportInterval: 0s\n",
		"spec.logging.level must be debug, info, warn or error": "  logging:\n    level: verbose\n",
		"spec.telemetry.serviceName must not be empty":          "  telemetry:\n    serviceName: \"\"\n",
		"spec.telemetry.endpoint must be an http or https URL":  "  telemetry:\n    endpoint: alloy:4317\n",
		"spec.shutdown: must be a mapping":                      "  shutdown: 5s\n",
	} {
		t.Run(want, func(t *testing.T) {
			useConfigFile(t, minimalFile+section)

			_, err := Load(testRole, testServiceName)
			require.ErrorContains(t, err, want)
		})
	}
}

// AC-1077: экспорт включён без endpoint — процесс не стартует.
func TestLoadRequiresTheEndpointForEnabledExport(t *testing.T) {
	for _, section := range []string{"tracing", "metrics"} {
		t.Run(section, func(t *testing.T) {
			useConfigFile(t, minimalFile+"  telemetry:\n    endpoint: \"\"\n  "+section+":\n    enabled: true\n")

			_, err := Load(testRole, testServiceName)
			require.ErrorContains(t, err, "spec.telemetry.endpoint is required")
		})
	}
}

// AC-1078: неизвестный протокол — ошибка перечисляет grpc и http/protobuf.
func TestLoadRejectsAnUnknownProtocol(t *testing.T) {
	useConfigFile(t, minimalFile+"  telemetry:\n    endpoint: http://alloy:4317\n    protocol: http/json\n")

	_, err := Load(testRole, testServiceName)
	require.ErrorContains(t, err, "grpc")
	require.ErrorContains(t, err, "http/protobuf")
	require.ErrorContains(t, err, "http/json")
}

// AC-1080: endpoint без флагов — валидная конфигурация, экспорт выключен.
func TestLoadAcceptsTheEndpointWithoutExport(t *testing.T) {
	useConfigFile(t, minimalFile+"  telemetry:\n    endpoint: http://alloy:4317\n")

	cfg, err := Load(testRole, testServiceName)
	require.NoError(t, err)
	assert.Equal(t, "http://alloy:4317", cfg.Telemetry.Endpoint)
	assert.False(t, cfg.Tracing.Enabled)
	assert.False(t, cfg.Metrics.Enabled)
}

// AC-1081: resourceAttributes не переопределяют идентичность процесса.
func TestLoadRejectsTheProcessIdentityInResourceAttributes(t *testing.T) {
	for _, key := range []string{"service.name", "service.instance.id"} {
		t.Run(key, func(t *testing.T) {
			useConfigFile(t, minimalFile+"  telemetry:\n    resourceAttributes:\n      "+key+": other\n")

			_, err := Load(testRole, testServiceName)
			require.ErrorContains(t, err, "spec.telemetry.resourceAttributes must not contain "+key)
		})
	}
}

// AC-1082: без POD_NAME процесс не стартует.
func TestLoadRequiresThePodName(t *testing.T) {
	useConfigFile(t, minimalFile)
	t.Setenv(EnvPodName, "")

	_, err := Load(testRole, testServiceName)
	require.ErrorContains(t, err, EnvPodName)
}
