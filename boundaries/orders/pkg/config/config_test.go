package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUsesDefaultPort(t *testing.T) {
	clearConfigEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, DefaultPort, cfg.Server.Port)
	assert.Equal(t, DefaultManagementPort, cfg.Management.Port)
	assert.Equal(t, DefaultDrainTimeout, cfg.Shutdown.DrainTimeout)
	assert.Equal(t, DefaultReadinessDelay, cfg.Shutdown.ReadinessDelay)
	assert.Equal(t, []string{DefaultKafkaBroker}, cfg.Kafka.Brokers)
	assert.Equal(t, DefaultKafkaClientID, cfg.Kafka.ClientID)
	assert.Equal(t, DefaultKafkaConsumerGroup, cfg.Kafka.ConsumerGroup)
}

func TestLoadEnvironmentOverridesConfigFile(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  port: 9000\n"), 0o600))
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("SERVER__PORT", "9001")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9001, cfg.Server.Port)
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("PORT", "70000")

	_, err := Load()
	require.Error(t, err)
}

func TestLoadReadsDrainTimeoutFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvDrainTimeout, "5s")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, cfg.Shutdown.DrainTimeout)
}

func TestLoadRejectsNonPositiveDrainTimeout(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvDrainTimeout, "0s")

	_, err := Load()
	require.Error(t, err)
}

func TestLoadRejectsManagementPortEqualToServerPort(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvManagementPort, "8081")

	_, err := Load()
	require.Error(t, err)
}

func TestLoadAllowsZeroReadinessDelay(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvReadinessDelay, "0s")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Zero(t, cfg.Shutdown.ReadinessDelay)
}

func TestLoadRejectsNegativeReadinessDelay(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvReadinessDelay, "-1s")

	_, err := Load()
	require.Error(t, err)
}

func TestLoadReadsKafkaFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvKafkaBrokers, "kafka-0:9092,kafka-1:9092")
	t.Setenv(EnvKafkaClientID, "orders-consumer")
	t.Setenv(EnvKafkaConsumerGroup, "orders-v1")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"kafka-0:9092", "kafka-1:9092"}, cfg.Kafka.Brokers)
	assert.Equal(t, "orders-consumer", cfg.Kafka.ClientID)
	assert.Equal(t, "orders-v1", cfg.Kafka.ConsumerGroup)
}

func TestLoadRejectsEmptyKafkaConsumerGroup(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvKafkaConsumerGroup, " ")

	_, err := Load()
	require.ErrorContains(t, err, "kafka consumer group")
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"CONFIG_FILE", EnvPort, "PORT", EnvManagementPort, EnvDrainTimeout, EnvReadinessDelay,
		EnvKafkaBrokers, EnvKafkaClientID, EnvKafkaConsumerGroup,
		EnvTemporalHostPort, EnvTemporalNamespace, EnvTemporalConnectTimeout, EnvTemporalRegisterSearchAttributes,
		EnvFulfillment("acceptance_timeout"), EnvFulfillment("max_replans"), EnvFulfillment("neighbours"),
		EnvFulfillment("publish_stage"), EnvInternalGatewayAddr, EnvCancellationWindow,
		EnvCatalogMinimumTotal, EnvCatalogMaximumTotal, EnvCatalogTimeout,
	} {
		value, exists := os.LookupEnv(name)
		require.NoError(t, os.Unsetenv(name))
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
	// The default grpc neighbours require a gateway address.
	t.Setenv(EnvInternalGatewayAddr, "internal-gateway:8080")
}

func TestLoadUsesTemporalAndFulfillmentDefaults(t *testing.T) {
	clearConfigEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, TemporalConfig{
		HostPort:       DefaultTemporalHostPort,
		Namespace:      DefaultTemporalNamespace,
		ConnectTimeout: DefaultTemporalConnectTimeout,
	}, cfg.Temporal)
	assert.Equal(t, FulfillmentConfig{
		AcceptanceTimeout:       10 * time.Minute,
		AssignmentTimeout:       15 * time.Minute,
		MaxReplans:              2,
		OperatorDecisionTimeout: 30 * time.Minute,
		PreparationTimeout:      90 * time.Minute,
		MaxPreparationExtension: 60 * time.Minute,
		PickupTimeout:           60 * time.Minute,
		DeliveryTimeout:         120 * time.Minute,
		PublishStage:            true,
		Neighbours:              NeighboursGRPC,
	}, cfg.Fulfillment)
}

func TestLoadReadsTemporalAndFulfillmentFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvTemporalHostPort, "temporal:7233")
	t.Setenv(EnvTemporalNamespace, "orders-local")
	t.Setenv(EnvTemporalRegisterSearchAttributes, "true")
	t.Setenv(EnvFulfillment("acceptance_timeout"), "2m")
	t.Setenv(EnvFulfillment("max_replans"), "0")
	t.Setenv(EnvFulfillment("publish_stage"), "false")
	t.Setenv(EnvFulfillment("neighbours"), "simulated")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "temporal:7233", cfg.Temporal.HostPort)
	assert.Equal(t, "orders-local", cfg.Temporal.Namespace)
	assert.True(t, cfg.Temporal.RegisterSearchAttributes)
	assert.Equal(t, 2*time.Minute, cfg.Fulfillment.AcceptanceTimeout)
	assert.Zero(t, cfg.Fulfillment.MaxReplans)
	assert.False(t, cfg.Fulfillment.PublishStage)
	assert.Equal(t, NeighboursSimulated, cfg.Fulfillment.Neighbours)
}

// AC-448: an invalid worker setting fails the load before startup.
func TestLoadRejectsInvalidWorkerSettings(t *testing.T) {
	tests := map[string]string{
		EnvFulfillment("acceptance_timeout"): "0s",
		EnvFulfillment("max_replans"):        "-1",
		EnvFulfillment("neighbours"):         "real",
		EnvTemporalNamespace:                 " ",
		EnvTemporalConnectTimeout:            "0s",
	}
	for env, value := range tests {
		t.Run(env, func(t *testing.T) {
			clearConfigEnvironment(t)
			t.Setenv(env, value)

			_, err := Load()
			require.Error(t, err)
		})
	}
}

func TestLoadRequiresGatewayForGRPCNeighbours(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvInternalGatewayAddr, "")

	_, err := Load()
	require.ErrorContains(t, err, EnvInternalGatewayAddr)

	t.Setenv(EnvInternalGatewayAddr, "internal-gateway:8080")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, NeighboursGRPC, cfg.Fulfillment.Neighbours)
	assert.Equal(t, "internal-gateway:8080", cfg.InternalGateway.Address)
}

func TestLoadUsesPlaceholderCancellationWindow(t *testing.T) {
	clearConfigEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, DefaultCancellationWindow, cfg.Cancellation.Window)
}

func TestLoadReadsCancellationWindowFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvCancellationWindow, "90s")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 90*time.Second, cfg.Cancellation.Window)
}

// AC-506: an unusable window stops the process at startup.
func TestLoadRejectsUnusableCancellationWindow(t *testing.T) {
	for _, value := range []string{"0s", "-1m", "soon"} {
		t.Run(value, func(t *testing.T) {
			clearConfigEnvironment(t)
			t.Setenv(EnvCancellationWindow, value)

			_, err := Load()
			require.Error(t, err)
		})
	}
}

func TestLoadUsesPlaceholderCatalogLimits(t *testing.T) {
	clearConfigEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, CatalogConfig{
		MinimumTotal: DefaultCatalogMinimumTotal,
		MaximumTotal: DefaultCatalogMaximumTotal,
		Timeout:      DefaultCatalogTimeout,
	}, cfg.Catalog)
}

func TestLoadReadsCatalogFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvCatalogMinimumTotal, "10.50")
	t.Setenv(EnvCatalogMaximumTotal, "500")
	t.Setenv(EnvCatalogTimeout, "3s")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, CatalogConfig{MinimumTotal: "10.50", MaximumTotal: "500", Timeout: 3 * time.Second}, cfg.Catalog)
}

func TestLoadRejectsUnusableCatalog(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"minimum not decimal": {EnvCatalogMinimumTotal: "ten"},
		"maximum not decimal": {EnvCatalogMaximumTotal: "lots"},
		"negative minimum":    {EnvCatalogMinimumTotal: "-1"},
		"maximum below min":   {EnvCatalogMinimumTotal: "20", EnvCatalogMaximumTotal: "10"},
		"zero timeout":        {EnvCatalogTimeout: "0s"},
	} {
		t.Run(name, func(t *testing.T) {
			clearConfigEnvironment(t)
			for key, value := range env {
				t.Setenv(key, value)
			}

			_, err := Load()
			require.ErrorContains(t, err, "catalog")
		})
	}
}
