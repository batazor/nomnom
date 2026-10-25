package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Management  ManagementConfig  `mapstructure:"management"`
	Shutdown    ShutdownConfig    `mapstructure:"shutdown"`
	Kafka       KafkaConfig       `mapstructure:"kafka"`
	Temporal    TemporalConfig    `mapstructure:"temporal"`
	Fulfillment FulfillmentConfig `mapstructure:"fulfillment"`

	InternalGateway InternalGatewayConfig `mapstructure:"internal_gateway"`
	Identity        IdentityConfig        `mapstructure:"identity"`
	SpiceDB         SpiceDBConfig         `mapstructure:"spicedb"`
	Postgres        PostgresConfig        `mapstructure:"postgres"`
	Reconciler      ReconcilerConfig      `mapstructure:"reconciler"`
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
	if err := configureTemporal(loader); err != nil {
		return Config{}, fmt.Errorf("configure temporal: %w", err)
	}
	if err := configureFulfillment(loader); err != nil {
		return Config{}, fmt.Errorf("configure fulfillment: %w", err)
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
	if err := configurePostgres(loader); err != nil {
		return Config{}, fmt.Errorf("configure postgres: %w", err)
	}
	if err := configureReconciler(loader); err != nil {
		return Config{}, fmt.Errorf("configure reconciler: %w", err)
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
	if err := cfg.Temporal.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Fulfillment.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Identity.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.SpiceDB.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Postgres.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.Reconciler.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.Fulfillment.Neighbours == NeighboursGRPC && strings.TrimSpace(cfg.InternalGateway.Address) == "" {
		return Config{}, fmt.Errorf("%s is required when fulfillment neighbours is %q", EnvInternalGatewayAddr, NeighboursGRPC)
	}

	return cfg, nil
}
