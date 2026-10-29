package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		EnvFulfillment("acceptance_timeout"), EnvFulfillment("pickup_timeout"),
		EnvFulfillment("publish_stage"), EnvInternalGatewayAddr, EnvCancellationWindow,
		EnvCatalogMinimumTotal, EnvCatalogMaximumTotal, EnvCatalogTimeout,
		EnvSimulatorInterval, EnvSimulatorMinDelay, EnvSimulatorMaxDelay, EnvSimulatorCustomerID, EnvSimulatorMarketIDs,
		EnvCancellationScheduledCutoff, EnvScheduledOrderPreparationBuffer, EnvScheduledOrderReplanLead,
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
	// The neighbours and the catalog require a gateway address; the plan and
	// the cancellation of a Scheduled Order have no defaults.
	t.Setenv(EnvInternalGatewayAddr, "internal-gateway:8080")
	t.Setenv(EnvCancellationScheduledCutoff, "60m")
	t.Setenv(EnvScheduledOrderPreparationBuffer, "5m")
	t.Setenv(EnvScheduledOrderReplanLead, "30m")
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
		PreparationTimeout:      90 * time.Minute,
		MaxPreparationExtension: 60 * time.Minute,
		PickupTimeout:           60 * time.Minute,
		DeliveryTimeout:         120 * time.Minute,
		PublishStage:            true,
	}, cfg.Fulfillment)
}

func TestLoadReadsTemporalAndFulfillmentFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvTemporalHostPort, "temporal:7233")
	t.Setenv(EnvTemporalNamespace, "orders-local")
	t.Setenv(EnvTemporalRegisterSearchAttributes, "true")
	t.Setenv(EnvFulfillment("acceptance_timeout"), "2m")
	t.Setenv(EnvFulfillment("pickup_timeout"), "45m")
	t.Setenv(EnvFulfillment("publish_stage"), "false")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "temporal:7233", cfg.Temporal.HostPort)
	assert.Equal(t, "orders-local", cfg.Temporal.Namespace)
	assert.True(t, cfg.Temporal.RegisterSearchAttributes)
	assert.Equal(t, 2*time.Minute, cfg.Fulfillment.AcceptanceTimeout)
	assert.Equal(t, 45*time.Minute, cfg.Fulfillment.PickupTimeout)
	assert.False(t, cfg.Fulfillment.PublishStage)
}

// AC-448: an invalid worker setting fails the load before startup.
func TestLoadRejectsInvalidWorkerSettings(t *testing.T) {
	tests := map[string]string{
		EnvFulfillment("acceptance_timeout"): "0s",
		EnvFulfillment("pickup_timeout"):     "0s",
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

func TestLoadRequiresGateway(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv(EnvInternalGatewayAddr, "")

	_, err := Load()
	require.ErrorContains(t, err, EnvInternalGatewayAddr)

	t.Setenv(EnvInternalGatewayAddr, "internal-gateway:8080")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "internal-gateway:8080", cfg.InternalGateway.Address)
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

func TestLoadReadsScheduledOrderFromEnvironment(t *testing.T) {
	clearConfigEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, ScheduledOrderConfig{PreparationBuffer: 5 * time.Minute, ReplanLead: 30 * time.Minute}, cfg.ScheduledOrder)
	assert.Equal(t, 60*time.Minute, cfg.Cancellation.ScheduledCutoff)
}

// AC-936: the plan parameters and the scheduled cancellation cutoff are
// required: empty, unparsable or non-positive values stop the process.
func TestLoadRejectsUnusableScheduledOrder(t *testing.T) {
	for _, env := range []string{EnvScheduledOrderPreparationBuffer, EnvScheduledOrderReplanLead, EnvCancellationScheduledCutoff} {
		for _, value := range []string{"", "0s", "-5m", "soon"} {
			t.Run(env+"="+value, func(t *testing.T) {
				clearConfigEnvironment(t)
				if value == "" {
					require.NoError(t, os.Unsetenv(env))
				} else {
					t.Setenv(env, value)
				}

				_, err := Load()
				require.Error(t, err)
			})
		}
	}
}
