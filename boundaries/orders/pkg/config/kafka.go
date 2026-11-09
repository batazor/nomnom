package config

import (
	"fmt"
	"strings"
)

const (
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
	Brokers []string `yaml:"brokers"`
	// ClientID is the service.name of the process: brokers use it for quotas and
	// diagnostics, so it never stays at the sarama default "watermill".
	ClientID      string `yaml:"clientId"`
	ConsumerGroup string `yaml:"consumerGroup"`
	// InitialOffset is decided per consumer group (ADR 28): oldest for groups that
	// must reflect the whole history, newest for groups that start fresh.
	InitialOffset string `yaml:"initialOffset"`
}

func (cfg KafkaConfig) Validate() error {
	if len(cfg.Brokers) == 0 {
		return fmt.Errorf("spec.kafka.brokers must not be empty")
	}
	for index, broker := range cfg.Brokers {
		if strings.TrimSpace(broker) == "" {
			return fmt.Errorf("spec.kafka.brokers[%d] must not be empty", index)
		}
	}
	if clientID := strings.TrimSpace(cfg.ClientID); clientID == "" || clientID == "watermill" {
		return fmt.Errorf("spec.kafka.clientId must be the service name")
	}
	if strings.TrimSpace(cfg.ConsumerGroup) == "" {
		return fmt.Errorf("spec.kafka.consumerGroup must not be empty")
	}
	if cfg.InitialOffset != KafkaOffsetOldest && cfg.InitialOffset != KafkaOffsetNewest {
		return fmt.Errorf("spec.kafka.initialOffset must be %q or %q, got %q", KafkaOffsetOldest, KafkaOffsetNewest, cfg.InitialOffset)
	}
	return nil
}
