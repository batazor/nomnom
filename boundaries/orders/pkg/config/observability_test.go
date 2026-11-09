package config

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUsesTelemetryDefaults(t *testing.T) {
	useConfig(t, document(RoleConsumer, spec()))

	cfg, err := Load(RoleConsumer)
	require.NoError(t, err)
	assert.Equal(t, ObservabilityConfig{
		Telemetry: TelemetryConfig{Protocol: ProtocolGRPC, ServiceName: RoleConsumer, InstanceID: "orders-7d9f-abcde"},
		Metrics:   MetricsConfig{ExportInterval: 60 * time.Second},
		Logging:   LoggingConfig{Level: "info"},
	}, cfg.Observability)
}

func TestLoadReadsTelemetry(t *testing.T) {
	s := spec()
	s["telemetry"] = map[string]any{
		"endpoint": "http://alloy.observability.svc:4318", "protocol": "http/protobuf", "serviceName": "orders-canary",
		"resourceAttributes": map[string]any{"deployment.environment.name": "kind", "service.version": "abc123"},
	}
	s["tracing"] = map[string]any{"enabled": true}
	s["metrics"] = map[string]any{"enabled": true, "exportInterval": "15s"}
	s["logging"] = map[string]any{"level": "debug"}
	useConfig(t, document(RoleAPI, s))
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://other:4317")
	t.Setenv("OTEL_SERVICE_NAME", "other")

	cfg, err := Load(RoleAPI)
	require.NoError(t, err)
	assert.Equal(t, ObservabilityConfig{
		Telemetry: TelemetryConfig{
			Endpoint: "http://alloy.observability.svc:4318", Protocol: ProtocolHTTPProtobuf, ServiceName: "orders-canary",
			ResourceAttributes: map[string]string{"deployment.environment.name": "kind", "service.version": "abc123"},
			InstanceID:         "orders-7d9f-abcde",
		},
		Tracing: TracingConfig{Enabled: true},
		Metrics: MetricsConfig{Enabled: true, ExportInterval: 15 * time.Second},
		Logging: LoggingConfig{Level: "debug"},
	}, cfg.Observability)
	level, err := cfg.Observability.Logging.SlogLevel()
	require.NoError(t, err)
	assert.Equal(t, slog.LevelDebug, level)
}

// AC-1080: an endpoint without flags is valid and enables no export.
func TestLoadAcceptsEndpointWithoutFlags(t *testing.T) {
	s := spec()
	s["telemetry"] = map[string]any{"endpoint": "http://alloy:4317"}
	useConfig(t, document(RoleAPI, s))

	cfg, err := Load(RoleAPI)
	require.NoError(t, err)
	assert.False(t, cfg.Observability.Tracing.Enabled)
	assert.False(t, cfg.Observability.Metrics.Enabled)
}

func TestLoadRejectsInvalidTelemetry(t *testing.T) {
	for name, tc := range map[string]struct {
		override map[string]any
		message  string
	}{
		// AC-1077
		"tracing without endpoint": {map[string]any{"tracing": map[string]any{"enabled": true}}, "spec.telemetry.endpoint is required"},
		"metrics without endpoint": {map[string]any{"metrics": map[string]any{"enabled": true}, "telemetry": map[string]any{"endpoint": ""}}, "spec.telemetry.endpoint is required"},
		// AC-1078
		"unknown protocol": {map[string]any{"telemetry": map[string]any{"protocol": "http/json"}}, `"grpc" or "http/protobuf"`},
		// AC-1081
		"service.name attribute":        {map[string]any{"telemetry": map[string]any{"resourceAttributes": map[string]any{"service.name": "x"}}}, "must not set service.name"},
		"service.instance.id attribute": {map[string]any{"telemetry": map[string]any{"resourceAttributes": map[string]any{"service.instance.id": "x"}}}, "must not set service.instance.id"},
		"endpoint not URL":              {map[string]any{"telemetry": map[string]any{"endpoint": "alloy:4317"}}, "spec.telemetry.endpoint must be an http or https URL"},
		"empty service name":            {map[string]any{"telemetry": map[string]any{"serviceName": ""}}, "spec.telemetry.serviceName"},
		"zero export interval":          {map[string]any{"metrics": map[string]any{"exportInterval": "0s"}}, "spec.metrics.exportInterval"},
		"unknown log level":             {map[string]any{"logging": map[string]any{"level": "verbose"}}, "spec.logging.level"},
		"empty log level":               {map[string]any{"logging": map[string]any{"level": ""}}, "spec.logging.level"},
	} {
		t.Run(name, func(t *testing.T) {
			s := spec()
			for section, value := range tc.override {
				s[section] = value
			}
			useConfig(t, document(RoleAPI, s))

			_, err := Load(RoleAPI)
			require.ErrorContains(t, err, tc.message)
		})
	}
}

// AC-1082: without POD_NAME there is no service.instance.id and the process does not start.
func TestLoadRequiresPodName(t *testing.T) {
	useConfig(t, document(RoleAPI, spec()))
	t.Setenv(EnvPodName, "")

	_, err := Load(RoleAPI)
	require.ErrorContains(t, err, EnvPodName)
}
