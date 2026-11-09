package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v4"

	"nomnom/orders/pkg/config/database"
)

// spec — обязательные секции без значений по умолчанию; тест дополняет их.
func spec() map[string]any {
	return map[string]any{
		"internalGateway": map[string]any{"address": "internal-gateway:8080"},
		"cancellation":    map[string]any{"scheduledCutoff": "60m"},
		"scheduledOrder":  map[string]any{"preparationBuffer": "5m", "replanLead": "30m"},
	}
}

func document(name string, spec map[string]any) map[string]any {
	return map[string]any{"apiVersion": APIVersion, "kind": Kind, "metadata": map[string]any{"name": name}, "spec": spec}
}

// useConfig пишет файл конфигурации и задаёт env процесса в pod: CONFIG_FILE,
// POD_NAME и секрет POSTGRES__DSN.
func useConfig(t *testing.T, doc map[string]any) {
	t.Helper()
	raw, err := yaml.Dump(doc)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	t.Setenv(EnvConfigFile, path)
	t.Setenv(EnvPodName, "orders-7d9f-abcde")
	t.Setenv(database.EnvPostgresDSN, "postgres://orders@db/orders")
}

// AC-1067: the file of its own role is accepted.
func TestLoadReadsFileOfItsRole(t *testing.T) {
	s := spec()
	s["shutdown"] = map[string]any{"readinessDelay": "2s", "drainTimeout": "15s"}
	s["temporal"] = map[string]any{"hostPort": "temporal:7233", "namespace": "orders-local"}
	useConfig(t, document(RoleWorker, s))

	cfg, err := Load(RoleWorker)
	require.NoError(t, err)
	assert.Equal(t, ShutdownConfig{ReadinessDelay: 2 * time.Second, DrainTimeout: 15 * time.Second}, cfg.Shutdown)
	assert.Equal(t, "temporal:7233", cfg.Temporal.HostPort)
	assert.Equal(t, "orders-local", cfg.Temporal.Namespace)
	assert.Equal(t, RoleWorker, cfg.Observability.Telemetry.ServiceName, "service.name defaults to the role")
}

// AC-1068: a foreign apiVersion or kind fails the load and names the expected ones.
func TestLoadRejectsForeignVersionOrKind(t *testing.T) {
	for name, header := range map[string][2]string{
		"version": {"config.nomnom.dev/v1beta1", Kind},
		"kind":    {APIVersion, "Delivery"},
	} {
		t.Run(name, func(t *testing.T) {
			doc := document(RoleAPI, spec())
			doc["apiVersion"], doc["kind"] = header[0], header[1]
			useConfig(t, doc)

			_, err := Load(RoleAPI)
			require.ErrorContains(t, err, APIVersion)
			require.ErrorContains(t, err, `"Orders"`)
		})
	}
}

// AC-1069: the file of another role is not accepted silently.
func TestLoadRejectsFileOfAnotherRole(t *testing.T) {
	useConfig(t, document(RoleWorker, spec()))

	_, err := Load(RoleConsumer)
	require.ErrorContains(t, err, "metadata.name must be \"orders-consumer\"")
}

// AC-1070: orders-admin runs in the pod of another role and reads its file.
func TestLoadAdminAcceptsFileOfAnyRole(t *testing.T) {
	s := spec()
	s["kafka"] = map[string]any{"brokers": []string{"kafka:9092"}, "clientId": "orders-consumer"}
	s["telemetry"] = map[string]any{"serviceName": RoleConsumer}
	useConfig(t, document(RoleConsumer, s))

	cfg, err := LoadAdmin("orders-admin")
	require.NoError(t, err)
	assert.Equal(t, []string{"kafka:9092"}, cfg.Kafka.Brokers)
	assert.Equal(t, "postgres://orders@db/orders", cfg.Database.DSN)
	assert.Equal(t, "orders-admin", cfg.Observability.Telemetry.ServiceName, "the command is its own service")

	doc := document(RoleConsumer, s)
	doc["kind"] = "Delivery"
	useConfig(t, doc)
	_, err = LoadAdmin("orders-admin")
	require.ErrorContains(t, err, APIVersion)
}

// AC-1071: without CONFIG_FILE or with a missing file the process does not start.
func TestLoadRequiresConfigFile(t *testing.T) {
	useConfig(t, document(RoleAPI, spec()))

	t.Setenv(EnvConfigFile, "")
	_, err := Load(RoleAPI)
	require.ErrorContains(t, err, EnvConfigFile)

	t.Setenv(EnvConfigFile, filepath.Join(t.TempDir(), "missing.yaml"))
	_, err = Load(RoleAPI)
	require.ErrorContains(t, err, "missing.yaml")
}

// AC-1072: a typo in a key fails the load with the path of the key.
func TestLoadRejectsUnknownKeyWithPath(t *testing.T) {
	s := spec()
	s["shutdown"] = map[string]any{"drainTimout": "20s"}
	useConfig(t, document(RoleAPI, s))

	_, err := Load(RoleAPI)
	require.ErrorContains(t, err, "spec.shutdown.drainTimout: unknown field")
}

// AC-1073: an unknown top-level key next to spec fails the load.
func TestLoadRejectsUnknownTopLevelKey(t *testing.T) {
	doc := document(RoleAPI, spec())
	doc["status"] = map[string]any{"ready": true}
	useConfig(t, doc)

	_, err := Load(RoleAPI)
	require.ErrorContains(t, err, "status: unknown field")
}

// Sections of the other roles of kind Orders are accepted.
func TestLoadAcceptsSectionsOfOtherRoles(t *testing.T) {
	s := spec()
	s["kafka"] = map[string]any{"clientId": "orders-consumer", "consumerGroup": "orders", "initialOffset": "newest"}
	s["schemaRegistry"] = map[string]any{"url": "http://schema-registry:8081"}
	s["temporal"] = map[string]any{"registerSearchAttributes": true}
	s["simulator"] = map[string]any{"interval": "5s"}
	useConfig(t, document(RoleWorker, s))

	cfg, err := Load(RoleWorker)
	require.NoError(t, err)
	assert.Equal(t, "http://schema-registry:8081", cfg.SchemaRegistry.URL)
	assert.Equal(t, KafkaOffsetNewest, cfg.Kafka.InitialOffset)
}

// AC-1074: the environment of a non-secret setting has no effect.
func TestLoadIgnoresEnvironmentOfNonSecretSettings(t *testing.T) {
	s := spec()
	s["shutdown"] = map[string]any{"drainTimeout": "20s"}
	useConfig(t, document(RoleAPI, s))
	t.Setenv("SHUTDOWN__DRAIN_TIMEOUT", "0ms")
	t.Setenv("SERVER__PORT", "9000")
	t.Setenv("PORT", "9001")
	t.Setenv("INTERNAL_GATEWAY_ADDR", "")
	t.Setenv("LOG__LEVEL", "verbose")
	t.Setenv("OTEL_SERVICE_NAME", "other")

	cfg, err := Load(RoleAPI)
	require.NoError(t, err)
	assert.Equal(t, 20*time.Second, cfg.Shutdown.DrainTimeout)
	assert.Equal(t, DefaultPort, cfg.Server.Port)
	assert.Equal(t, "internal-gateway:8080", cfg.InternalGateway.Address)
	assert.Equal(t, DefaultLogLevel, cfg.Observability.Logging.Level)
	assert.Equal(t, RoleAPI, cfg.Observability.Telemetry.ServiceName)
}

// AC-1075: a secret in the file is an unknown key.
func TestLoadRejectsSecretInFile(t *testing.T) {
	for section, key := range map[string]string{"postgres": "dsn", "spicedb": "presharedKey"} {
		t.Run(section, func(t *testing.T) {
			s := spec()
			s[section] = map[string]any{key: "secret"}
			useConfig(t, document(RoleAPI, s))

			_, err := Load(RoleAPI)
			require.ErrorContains(t, err, "spec."+section+"."+key+": unknown field")
		})
	}
}

// Secrets come from the env of the pod.
func TestLoadReadsSecretsFromEnvironment(t *testing.T) {
	s := spec()
	s["spicedb"] = map[string]any{"endpoint": "spicedb:50051", "insecure": true}
	useConfig(t, document(RoleAPI, s))
	t.Setenv(EnvSpiceDBPresharedKey, "devkey")

	cfg, err := Load(RoleAPI)
	require.NoError(t, err)
	assert.Equal(t, "postgres://orders@db/orders", cfg.Database.DSN)
	assert.Equal(t, SpiceDBConfig{Endpoint: "spicedb:50051", PresharedKey: "devkey", Insecure: true, Timeout: DefaultSpiceDBTimeout}, cfg.SpiceDB)

	t.Setenv(EnvSpiceDBPresharedKey, "")
	_, err = Load(RoleAPI)
	require.ErrorContains(t, err, EnvSpiceDBPresharedKey)
}

// AC-1076: an empty POSTGRES__DSN fails the load.
func TestLoadRequiresPostgresDSN(t *testing.T) {
	useConfig(t, document(RoleAPI, spec()))
	t.Setenv(database.EnvPostgresDSN, "")

	_, err := Load(RoleAPI)
	require.ErrorContains(t, err, database.EnvPostgresDSN)
}

// The Worker Deployment Version comes from the env of Temporal Worker Controller.
func TestLoadReadsWorkerVersionFromEnvironment(t *testing.T) {
	useConfig(t, document(RoleWorker, spec()))
	t.Setenv(EnvTemporalDeploymentName, "nomnom-orders/orders-worker")
	t.Setenv(EnvTemporalWorkerBuildID, "orders-1a2b3c")

	cfg, err := Load(RoleWorker)
	require.NoError(t, err)
	assert.Equal(t, "nomnom-orders/orders-worker", cfg.Temporal.DeploymentName)
	assert.Equal(t, "orders-1a2b3c", cfg.Temporal.BuildID)
	require.NoError(t, cfg.Temporal.ValidateVersion())
}

func TestLoadUsesDefaults(t *testing.T) {
	useConfig(t, document(RoleAPI, spec()))

	cfg, err := Load(RoleAPI)
	require.NoError(t, err)
	assert.Equal(t, DefaultPort, cfg.Server.Port)
	assert.Equal(t, DefaultManagementPort, cfg.Management.Port)
	assert.Equal(t, ShutdownConfig{ReadinessDelay: DefaultReadinessDelay, DrainTimeout: DefaultDrainTimeout}, cfg.Shutdown)
	assert.Equal(t, KafkaConfig{Brokers: []string{DefaultKafkaBroker}, ClientID: DefaultKafkaClientID, ConsumerGroup: DefaultKafkaConsumerGroup, InitialOffset: KafkaOffsetOldest}, cfg.Kafka)
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
	assert.Equal(t, CatalogConfig{MinimumTotal: "0", MaximumTotal: "100000", Timeout: 2 * time.Second}, cfg.Catalog)
	assert.Equal(t, DefaultCancellationWindow, cfg.Cancellation.Window)
	assert.Equal(t, database.Config{
		DSN:                   "postgres://orders@db/orders",
		MaxConns:              database.DefaultMaxConns,
		MinConns:              database.DefaultMinConns,
		MaxConnLifetime:       30 * time.Minute,
		MaxConnLifetimeJitter: 5 * time.Minute,
		MaxConnIdleTime:       5 * time.Minute,
		ConnectTimeout:        5 * time.Second,
		HealthCheckPeriod:     time.Minute,
	}, cfg.Database)
	assert.Equal(t, DefaultIdentityAudience, cfg.Identity.Audience)
}

func TestLoadReadsSections(t *testing.T) {
	s := spec()
	s["kafka"] = map[string]any{"brokers": []string{"kafka-0:9092", "kafka-1:9092"}, "clientId": "orders-consumer", "consumerGroup": "orders-v1"}
	s["temporal"] = map[string]any{"hostPort": "temporal:7233", "namespace": "orders-local", "registerSearchAttributes": true}
	s["fulfillment"] = map[string]any{"acceptanceTimeout": "2m", "pickupTimeout": "45m", "publishStage": false}
	s["cancellation"] = map[string]any{"window": "90s", "scheduledCutoff": "60m"}
	s["catalog"] = map[string]any{"minimumTotal": "10.50", "maximumTotal": 500, "timeout": "3s"}
	s["postgres"] = map[string]any{
		"maxConns": 3, "minConns": 1, "maxConnLifetime": "20m", "maxConnLifetimeJitter": "0s",
		"maxConnIdleTime": "90s", "connectTimeout": "2s", "healthCheckPeriod": "30s",
	}
	s["identity"] = map[string]any{"userIssuer": "nomnom-bff", "userJwksFile": "/etc/nomnom/user-identity/jwks.json", "userDelegates": " bff, ,admin-api "}
	useConfig(t, document(RoleConsumer, s))

	cfg, err := Load(RoleConsumer)
	require.NoError(t, err)
	assert.Equal(t, KafkaConfig{Brokers: []string{"kafka-0:9092", "kafka-1:9092"}, ClientID: "orders-consumer", ConsumerGroup: "orders-v1", InitialOffset: KafkaOffsetOldest}, cfg.Kafka)
	assert.Equal(t, "temporal:7233", cfg.Temporal.HostPort)
	assert.Equal(t, "orders-local", cfg.Temporal.Namespace)
	assert.True(t, cfg.Temporal.RegisterSearchAttributes)
	assert.Equal(t, 2*time.Minute, cfg.Fulfillment.AcceptanceTimeout)
	assert.Equal(t, 45*time.Minute, cfg.Fulfillment.PickupTimeout)
	assert.Equal(t, 90*time.Minute, cfg.Fulfillment.PreparationTimeout, "omitted keys keep their default")
	assert.False(t, cfg.Fulfillment.PublishStage)
	assert.Equal(t, CancellationConfig{Window: 90 * time.Second, ScheduledCutoff: 60 * time.Minute}, cfg.Cancellation)
	assert.Equal(t, ScheduledOrderConfig{PreparationBuffer: 5 * time.Minute, ReplanLead: 30 * time.Minute}, cfg.ScheduledOrder)
	assert.Equal(t, CatalogConfig{MinimumTotal: "10.50", MaximumTotal: "500", Timeout: 3 * time.Second}, cfg.Catalog)
	assert.Equal(t, database.Config{
		DSN:                   "postgres://orders@db/orders",
		MaxConns:              3,
		MinConns:              1,
		MaxConnLifetime:       20 * time.Minute,
		MaxConnLifetimeJitter: 0,
		MaxConnIdleTime:       90 * time.Second,
		ConnectTimeout:        2 * time.Second,
		HealthCheckPeriod:     30 * time.Second,
	}, cfg.Database)
	assert.Equal(t, "nomnom-bff", cfg.Identity.UserIssuer)
	assert.Equal(t, map[string]bool{"bff": true, "admin-api": true}, cfg.Identity.Delegates())
	assert.True(t, cfg.Identity.UserVerifying())
}

// AC-448, AC-506, AC-936 and the runtime contract: an unusable setting fails the load before startup.
func TestLoadRejectsInvalidSettings(t *testing.T) {
	for name, override := range map[string]map[string]any{
		"server port":             {"server": map[string]any{"port": 70000}},
		"zero drain timeout":      {"shutdown": map[string]any{"drainTimeout": "0ms"}},
		"negative readiness":      {"shutdown": map[string]any{"readinessDelay": "-1s"}},
		"management = server":     {"management": map[string]any{"port": 8081}},
		"empty consumer group":    {"kafka": map[string]any{"consumerGroup": " "}},
		"unknown initial offset":  {"kafka": map[string]any{"initialOffset": "latest"}},
		"zero acceptance":         {"fulfillment": map[string]any{"acceptanceTimeout": "0s"}},
		"zero pickup":             {"fulfillment": map[string]any{"pickupTimeout": "0s"}},
		"blank temporal ns":       {"temporal": map[string]any{"namespace": " "}},
		"zero temporal connect":   {"temporal": map[string]any{"connectTimeout": "0s"}},
		"no gateway":              {"internalGateway": map[string]any{"address": ""}},
		"zero window":             {"cancellation": map[string]any{"window": "0s", "scheduledCutoff": "60m"}},
		"negative window":         {"cancellation": map[string]any{"window": "-1m", "scheduledCutoff": "60m"}},
		"unparsable window":       {"cancellation": map[string]any{"window": "soon", "scheduledCutoff": "60m"}},
		"integer duration":        {"cancellation": map[string]any{"window": 300, "scheduledCutoff": "60m"}},
		"no cutoff":               {"cancellation": map[string]any{}},
		"no preparation buffer":   {"scheduledOrder": map[string]any{"replanLead": "30m"}},
		"zero replan lead":        {"scheduledOrder": map[string]any{"preparationBuffer": "5m", "replanLead": "0s"}},
		"catalog not decimal":     {"catalog": map[string]any{"minimumTotal": "ten"}},
		"catalog negative min":    {"catalog": map[string]any{"minimumTotal": "-1"}},
		"catalog max below min":   {"catalog": map[string]any{"minimumTotal": "20", "maximumTotal": "10"}},
		"catalog zero timeout":    {"catalog": map[string]any{"timeout": "0s"}},
		"zero max conns":          {"postgres": map[string]any{"maxConns": 0}},
		"min conns above one":     {"postgres": map[string]any{"minConns": 2}},
		"negative jitter":         {"postgres": map[string]any{"maxConnLifetimeJitter": "-1s"}},
		"zero health check":       {"postgres": map[string]any{"healthCheckPeriod": "0s"}},
		"malformed pool duration": {"postgres": map[string]any{"connectTimeout": "five"}},
		"issuer without accounts": {"identity": map[string]any{"issuer": "https://kubernetes.default.svc"}},
	} {
		t.Run(name, func(t *testing.T) {
			s := spec()
			for section, value := range override {
				s[section] = value
			}
			useConfig(t, document(RoleAPI, s))

			_, err := Load(RoleAPI)
			require.Error(t, err)
		})
	}
}

// config.example.yaml (make run) stays a valid file of the API.
func TestConfigExampleIsValid(t *testing.T) {
	t.Setenv(EnvConfigFile, filepath.Join("..", "..", "config.example.yaml"))
	t.Setenv(EnvPodName, "orders-local")
	t.Setenv(database.EnvPostgresDSN, "postgres://orders@localhost/orders")
	t.Setenv(EnvSpiceDBPresharedKey, "devkey")

	cfg, err := Load(RoleAPI)
	require.NoError(t, err)
	require.NoError(t, cfg.Simulator.Validate())
}

func TestLoadAllowsZeroReadinessDelay(t *testing.T) {
	s := spec()
	s["shutdown"] = map[string]any{"readinessDelay": "0s"}
	useConfig(t, document(RoleAPI, s))

	cfg, err := Load(RoleAPI)
	require.NoError(t, err)
	assert.Zero(t, cfg.Shutdown.ReadinessDelay)
}
