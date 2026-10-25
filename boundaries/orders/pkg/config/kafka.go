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
	configKeyKafkaInitialOffset = "kafka.initial_offset"

	EnvKafkaBrokers       = "KAFKA__BROKERS"
	EnvKafkaClientID      = "KAFKA__CLIENT_ID"
	EnvKafkaConsumerGroup = "KAFKA__CONSUMER_GROUP"
	EnvKafkaInitialOffset = "KAFKA__INITIAL_OFFSET"

	DefaultKafkaBroker        = "localhost:9092"
	DefaultKafkaClientID      = "orders"
	DefaultKafkaConsumerGroup = "orders"
	DefaultKafkaInitialOffset = KafkaOffsetOldest
)

// Where a consumer group without committed offsets starts reading a stream.
const (
	// KafkaOffsetOldest replays the stream from the beginning through the Inbox.
	KafkaOffsetOldest = "oldest"
	// KafkaOffsetNewest skips the history and reads only new events.
	KafkaOffsetNewest = "newest"
)

// KafkaConfig is shared by Kafka-backed process composition roots. The HTTP
// process loads it but does not connect to Kafka.
type KafkaConfig struct {
	Brokers []string `mapstructure:"brokers"`
	// ClientID is the service.name of the process: brokers use it for quotas and
	// diagnostics, so it never stays at the sarama default "watermill".
	ClientID      string `mapstructure:"client_id"`
	ConsumerGroup string `mapstructure:"consumer_group"`
	// InitialOffset is decided per consumer group (ADR 28): oldest for groups that
	// must reflect the whole history, newest for groups that start fresh.
	InitialOffset string `mapstructure:"initial_offset"`
}

func configureKafka(loader *viper.Viper) error {
	loader.SetDefault(configKeyKafkaBrokers, []string{DefaultKafkaBroker})
	loader.SetDefault(configKeyKafkaClientID, DefaultKafkaClientID)
	loader.SetDefault(configKeyKafkaConsumerGroup, DefaultKafkaConsumerGroup)
	loader.SetDefault(configKeyKafkaInitialOffset, DefaultKafkaInitialOffset)

	for key, env := range map[string]string{
		configKeyKafkaBrokers:       EnvKafkaBrokers,
		configKeyKafkaClientID:      EnvKafkaClientID,
		configKeyKafkaConsumerGroup: EnvKafkaConsumerGroup,
		configKeyKafkaInitialOffset: EnvKafkaInitialOffset,
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
	if clientID := strings.TrimSpace(cfg.ClientID); clientID == "" || clientID == "watermill" {
		return fmt.Errorf("kafka client id must be the service name")
	}
	if strings.TrimSpace(cfg.ConsumerGroup) == "" {
		return fmt.Errorf("kafka consumer group must not be empty")
	}
	if cfg.InitialOffset != KafkaOffsetOldest && cfg.InitialOffset != KafkaOffsetNewest {
		return fmt.Errorf("kafka initial offset must be %q or %q", KafkaOffsetOldest, KafkaOffsetNewest)
	}
	return nil
}
