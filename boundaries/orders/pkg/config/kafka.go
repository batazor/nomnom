package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

const (
	configKeyKafkaBrokers       = "kafka.brokers"
	configKeyKafkaClientID      = "kafka.client_id"
	configKeyKafkaConsumerGroup = "kafka.consumer_group"
	configKeyKafkaPoisonTopic   = "kafka.poison_topic"

	EnvKafkaBrokers       = "KAFKA__BROKERS"
	EnvKafkaClientID      = "KAFKA__CLIENT_ID"
	EnvKafkaConsumerGroup = "KAFKA__CONSUMER_GROUP"
	EnvKafkaPoisonTopic   = "KAFKA__POISON_TOPIC"

	DefaultKafkaBroker        = "localhost:9092"
	DefaultKafkaClientID      = "orders"
	DefaultKafkaConsumerGroup = "orders"
	DefaultKafkaPoisonTopic   = "orders.v1.consumer-poison"
)

// KafkaConfig is shared by Kafka-backed process composition roots. The HTTP
// process loads it but does not connect to Kafka.
type KafkaConfig struct {
	Brokers       []string `mapstructure:"brokers"`
	ClientID      string   `mapstructure:"client_id"`
	ConsumerGroup string   `mapstructure:"consumer_group"`
	// PoisonTopic receives inbound messages that cannot be processed (ADR 28).
	PoisonTopic string `mapstructure:"poison_topic"`
}

func configureKafka(loader *viper.Viper) error {
	loader.SetDefault(configKeyKafkaBrokers, []string{DefaultKafkaBroker})
	loader.SetDefault(configKeyKafkaClientID, DefaultKafkaClientID)
	loader.SetDefault(configKeyKafkaConsumerGroup, DefaultKafkaConsumerGroup)
	loader.SetDefault(configKeyKafkaPoisonTopic, DefaultKafkaPoisonTopic)

	for key, env := range map[string]string{
		configKeyKafkaBrokers:       EnvKafkaBrokers,
		configKeyKafkaClientID:      EnvKafkaClientID,
		configKeyKafkaConsumerGroup: EnvKafkaConsumerGroup,
		configKeyKafkaPoisonTopic:   EnvKafkaPoisonTopic,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg KafkaConfig) Validate() error {
	if len(cfg.Brokers) == 0 {
		return fmt.Errorf("kafka brokers must not be empty")
	}
	for index, broker := range cfg.Brokers {
		if strings.TrimSpace(broker) == "" {
			return fmt.Errorf("kafka broker at index %d must not be empty", index)
		}
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return fmt.Errorf("kafka client id must not be empty")
	}
	if strings.TrimSpace(cfg.ConsumerGroup) == "" {
		return fmt.Errorf("kafka consumer group must not be empty")
	}
	if strings.TrimSpace(cfg.PoisonTopic) == "" {
		return fmt.Errorf("kafka poison topic must not be empty")
	}
	return nil
}
