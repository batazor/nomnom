package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"

	"nomnom/orders/pkg/config/database"
)

// LoadDatabase reads only the postgres section: a one-off command that needs
// the database (orders-admin projections, its hook Job) does not carry the
// configuration of the API.
func LoadDatabase() (database.Config, error) {
	loader, err := sectionLoader(database.Configure)
	if err != nil {
		return database.Config{}, err
	}
	var cfg struct {
		Postgres database.Config `mapstructure:"postgres"`
	}
	if err := loader.Unmarshal(&cfg); err != nil {
		return database.Config{}, fmt.Errorf("decode postgres config: %w", err)
	}
	return cfg.Postgres, cfg.Postgres.Validate()
}

// LoadSpiceDB reads only the spicedb section, for orders-admin authz; the
// endpoint is required.
func LoadSpiceDB() (SpiceDBConfig, error) {
	loader, err := sectionLoader(configureSpiceDB)
	if err != nil {
		return SpiceDBConfig{}, err
	}
	var cfg struct {
		SpiceDB SpiceDBConfig `mapstructure:"spicedb"`
	}
	if err := loader.Unmarshal(&cfg); err != nil {
		return SpiceDBConfig{}, fmt.Errorf("decode spicedb config: %w", err)
	}
	if strings.TrimSpace(cfg.SpiceDB.Endpoint) == "" {
		return SpiceDBConfig{}, fmt.Errorf("%s is required", EnvSpiceDBEndpoint)
	}
	return cfg.SpiceDB, cfg.SpiceDB.Validate()
}

// LoadKafka reads only the kafka section, for orders-admin dlq.
func LoadKafka() (KafkaConfig, error) {
	loader, err := sectionLoader(configureKafka)
	if err != nil {
		return KafkaConfig{}, err
	}
	var cfg struct {
		Kafka KafkaConfig `mapstructure:"kafka"`
	}
	if err := loader.Unmarshal(&cfg); err != nil {
		return KafkaConfig{}, fmt.Errorf("decode kafka config: %w", err)
	}
	return cfg.Kafka, cfg.Kafka.Validate()
}

func sectionLoader(configure func(*viper.Viper) error) (*viper.Viper, error) {
	loader := viper.New()
	loader.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))
	if err := configure(loader); err != nil {
		return nil, err
	}
	if path := os.Getenv("CONFIG_FILE"); path != "" {
		loader.SetConfigFile(path)
		if err := loader.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}
	return loader, nil
}
