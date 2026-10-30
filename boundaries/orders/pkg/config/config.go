package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"

	"nomnom/orders/pkg/config/database"
)

type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	Management ManagementConfig `mapstructure:"management"`
	Shutdown   ShutdownConfig   `mapstructure:"shutdown"`
	Kafka      KafkaConfig      `mapstructure:"kafka"`
	// SchemaRegistry is read by orders-consumer only (FR-715).
	SchemaRegistry SchemaRegistryConfig `mapstructure:"schema_registry"`
	Temporal       TemporalConfig       `mapstructure:"temporal"`
	Fulfillment    FulfillmentConfig    `mapstructure:"fulfillment"`
	// ScheduledOrder is the plan of a Scheduled Order (FR-677, FR-693).
	ScheduledOrder ScheduledOrderConfig `mapstructure:"scheduled_order"`

	Cancellation CancellationConfig `mapstructure:"cancellation"`
	Catalog      CatalogConfig      `mapstructure:"catalog"`

	InternalGateway InternalGatewayConfig `mapstructure:"internal_gateway"`
	Identity        IdentityConfig        `mapstructure:"identity"`
	SpiceDB         SpiceDBConfig         `mapstructure:"spicedb"`
	Database        database.Config       `mapstructure:"postgres"`
	Simulator       SimulatorConfig       `mapstructure:"simulator"`
}

func Load() (Config, error) {
	loader := viper.New()
	loader.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))

	if err := configureServer(loader); err != nil {
		return Config{}, fmt.Errorf("configure server: %w", err)
	}
	if err := configureManagement(loader); err != nil {
		return Config{}, fmt.Errorf("configure management: %w", err)
	}
	if err := configureShutdown(loader); err != nil {
		return Config{}, fmt.Errorf("configure shutdown: %w", err)
	}
	if err := configureKafka(loader); err != nil {
		return Config{}, fmt.Errorf("configure kafka: %w", err)
	}
	if err := configureSchemaRegistry(loader); err != nil {
		return Config{}, fmt.Errorf("configure schema registry: %w", err)
	}
	if err := configureTemporal(loader); err != nil {
		return Config{}, fmt.Errorf("configure temporal: %w", err)
	}
	if err := configureFulfillment(loader); err != nil {
		return Config{}, fmt.Errorf("configure fulfillment: %w", err)
	}
	if err := configureScheduledOrder(loader); err != nil {
		return Config{}, fmt.Errorf("configure scheduled order: %w", err)
	}
	if err := configureCancellation(loader); err != nil {
		return Config{}, fmt.Errorf("configure cancellation: %w", err)
	}
	if err := configureCatalog(loader); err != nil {
		return Config{}, fmt.Errorf("configure catalog: %w", err)
	}
	if err := configureInternalGateway(loader); err != nil {
		return Config{}, fmt.Errorf("configure internal gateway: %w", err)
	}
	if err := configureIdentity(loader); err != nil {
		return Config{}, fmt.Errorf("configure identity: %w", err)
	}
	if err := configureSpiceDB(loader); err != nil {
		return Config{}, fmt.Errorf("configure spicedb: %w", err)
	}
	if err := database.Configure(loader); err != nil {
		return Config{}, fmt.Errorf("configure database: %w", err)
	}

	if err := configureSimulator(loader); err != nil {
		return Config{}, fmt.Errorf("configure simulator: %w", err)
	}

	if path := os.Getenv("CONFIG_FILE"); path != "" {
		loader.SetConfigFile(path)
		if err := loader.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := loader.UnmarshalExact(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Server.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Management.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.Management.Port == cfg.Server.Port {
		return Config{}, fmt.Errorf("management port must differ from server port %d", cfg.Server.Port)
	}
	if err := cfg.Shutdown.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Kafka.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.SchemaRegistry.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Temporal.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Fulfillment.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.ScheduledOrder.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Cancellation.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Catalog.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Identity.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.SpiceDB.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Database.Validate(); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.InternalGateway.Address) == "" {
		return Config{}, fmt.Errorf("%s is required", EnvInternalGatewayAddr)
	}

	return cfg, nil
}
