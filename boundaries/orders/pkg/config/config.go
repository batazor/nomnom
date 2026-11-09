// Package config — конфигурация процессов orders. Несекретные настройки читаются
// только из версионированного файла CONFIG_FILE (apiVersion
// config.nomnom.dev/v1alpha1, kind Orders, metadata.name — роль процесса,
// spec — секции); секреты — только из env под своими именами (POSTGRES__DSN,
// SPICEDB__PRESHARED_KEY), полей для них в spec нет.
package config

import (
	"fmt"
	"strings"

	"nomnom/orders/pkg/config/database"
)

// Роли процессов orders: metadata.name файла конфигурации и service.name по
// умолчанию. orders-admin запускается в pod другой роли и роль не сверяет.
const (
	RoleAPI      = "orders"
	RoleWorker   = "orders-worker"
	RoleConsumer = "orders-consumer"
)

// Config — spec файла конфигурации kind Orders. Все роли делят одну структуру:
// процесс проверяет весь spec на неизвестные ключи, а секции других ролей
// принимает.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Management ManagementConfig `yaml:"management"`
	Shutdown   ShutdownConfig   `yaml:"shutdown"`
	Kafka      KafkaConfig      `yaml:"kafka"`
	// SchemaRegistry is read by orders-consumer only (FR-715).
	SchemaRegistry SchemaRegistryConfig `yaml:"schemaRegistry"`
	Temporal       TemporalConfig       `yaml:"temporal"`
	Fulfillment    FulfillmentConfig    `yaml:"fulfillment"`
	// ScheduledOrder is the plan of a Scheduled Order (FR-677, FR-693).
	ScheduledOrder ScheduledOrderConfig `yaml:"scheduledOrder"`

	Cancellation CancellationConfig `yaml:"cancellation"`
	Catalog      CatalogConfig      `yaml:"catalog"`

	InternalGateway InternalGatewayConfig `yaml:"internalGateway"`
	Identity        IdentityConfig        `yaml:"identity"`
	SpiceDB         SpiceDBConfig         `yaml:"spicedb"`
	Database        database.Config       `yaml:"postgres"`
	Simulator       SimulatorConfig       `yaml:"simulator"`

	// Observability — секции telemetry, tracing, metrics и logging верхнего уровня spec.
	Observability ObservabilityConfig `yaml:",inline"`
}

// defaults — явные значения по умолчанию; обязательные настройки без них
// (cancellation.scheduledCutoff, scheduledOrder.*, simulator.*, identity.*,
// spicedb.endpoint, internalGateway.address) остаются пустыми.
func defaults(serviceName string) Config {
	return Config{
		Server:         ServerConfig{Port: DefaultPort},
		Management:     ManagementConfig{Port: DefaultManagementPort},
		Shutdown:       ShutdownConfig{ReadinessDelay: DefaultReadinessDelay, DrainTimeout: DefaultDrainTimeout},
		Kafka:          KafkaConfig{Brokers: []string{DefaultKafkaBroker}, ClientID: DefaultKafkaClientID, ConsumerGroup: DefaultKafkaConsumerGroup, InitialOffset: DefaultKafkaInitialOffset},
		SchemaRegistry: SchemaRegistryConfig{Timeout: DefaultSchemaRegistryTimeout},
		Temporal:       TemporalConfig{HostPort: DefaultTemporalHostPort, Namespace: DefaultTemporalNamespace, ConnectTimeout: DefaultTemporalConnectTimeout},
		Fulfillment:    DefaultFulfillment,
		Cancellation:   CancellationConfig{Window: DefaultCancellationWindow},
		Catalog:        CatalogConfig{MinimumTotal: DefaultCatalogMinimumTotal, MaximumTotal: DefaultCatalogMaximumTotal, Timeout: DefaultCatalogTimeout},
		Identity:       IdentityConfig{Audience: DefaultIdentityAudience},
		SpiceDB:        SpiceDBConfig{Timeout: DefaultSpiceDBTimeout},
		Database:       database.Defaults,
		Observability:  defaultObservability(serviceName),
	}
}

// Load читает файл конфигурации процесса role: metadata.name файла должен
// совпадать с ролью. service.name по умолчанию — имя роли.
func Load(role string) (Config, error) {
	cfg, err := read(role, func(name string) error {
		if name != role {
			return fmt.Errorf("metadata.name must be %q, the role of the process, got %q", role, name)
		}
		return nil
	})
	if err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) validate() error {
	if err := cfg.Server.Validate(); err != nil {
		return err
	}
	if err := cfg.Management.Validate(); err != nil {
		return err
	}
	if cfg.Management.Port == cfg.Server.Port {
		return fmt.Errorf("spec.management.port must differ from spec.server.port %d", cfg.Server.Port)
	}
	if err := cfg.Shutdown.Validate(); err != nil {
		return err
	}
	if err := cfg.Kafka.Validate(); err != nil {
		return err
	}
	if err := cfg.SchemaRegistry.Validate(); err != nil {
		return err
	}
	if err := cfg.Temporal.Validate(); err != nil {
		return err
	}
	if err := cfg.Fulfillment.Validate(); err != nil {
		return err
	}
	if err := cfg.ScheduledOrder.Validate(); err != nil {
		return err
	}
	if err := cfg.Cancellation.Validate(); err != nil {
		return err
	}
	if err := cfg.Catalog.Validate(); err != nil {
		return err
	}
	if err := cfg.Identity.Validate(); err != nil {
		return err
	}
	if err := cfg.SpiceDB.Validate(); err != nil {
		return err
	}
	if err := cfg.Database.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.InternalGateway.Address) == "" {
		return fmt.Errorf("%s is required", KeyInternalGatewayAddress)
	}
	return cfg.Observability.Validate()
}
