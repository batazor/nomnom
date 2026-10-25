// Package messaging owns the Watermill/Kafka transport conventions shared by
// orders publishers, forwarders and consumers.
package messaging

import (
	"fmt"

	"github.com/IBM/sarama"
	"github.com/ThreeDotsLabs/watermill"
	watermillkafka "github.com/ThreeDotsLabs/watermill-kafka/v3/pkg/kafka"
	"github.com/ThreeDotsLabs/watermill/message"

	"nomnom/orders/pkg/config"
)

const (
	EventIDMetadata      = "event_id"
	PartitionKeyMetadata = "order_id"
)

// NewEventMessage builds the transport representation required by the Inbox
// and ordering ADRs: UUID and event_id are identical, and order_id is the Kafka
// partition key.
func NewEventMessage(eventID, orderID string, payload []byte) (*message.Message, error) {
	if eventID == "" {
		return nil, fmt.Errorf("event id must not be empty")
	}
	if orderID == "" {
		return nil, fmt.Errorf("order id must not be empty")
	}

	msg := message.NewMessage(eventID, payload)
	msg.Metadata.Set(EventIDMetadata, eventID)
	msg.Metadata.Set(PartitionKeyMetadata, orderID)
	return msg, nil
}

func NewPublisher(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (*watermillkafka.Publisher, error) {
	return newPublisher(cfg, logger, func(_ string, msg *message.Message) (string, error) {
		key := msg.Metadata.Get(PartitionKeyMetadata)
		if key == "" {
			return "", fmt.Errorf("message %q has no %s metadata", msg.UUID, PartitionKeyMetadata)
		}
		return key, nil
	})
}

// NewPoisonPublisher publishes messages rejected by the consumer to the poison
// topic, keyed by the aggregate_id of the source stream when it is known.
func NewPoisonPublisher(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (*watermillkafka.Publisher, error) {
	return newPublisher(cfg, logger, func(_ string, msg *message.Message) (string, error) {
		if key := msg.Metadata.Get(AggregateIDMetadata); key != "" {
			return key, nil
		}
		return msg.UUID, nil
	})
}

func newPublisher(cfg config.KafkaConfig, logger watermill.LoggerAdapter, key watermillkafka.GeneratePartitionKey) (*watermillkafka.Publisher, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	saramaConfig := watermillkafka.DefaultSaramaSyncPublisherConfig()
	saramaConfig.ClientID = cfg.ClientID
	saramaConfig.Producer.RequiredAcks = sarama.WaitForAll
	saramaConfig.Producer.Idempotent = true
	saramaConfig.Net.MaxOpenRequests = 1

	marshaler := watermillkafka.NewWithPartitioningMarshaler(key)

	publisher, err := watermillkafka.NewPublisher(watermillkafka.PublisherConfig{
		Brokers:               cfg.Brokers,
		Marshaler:             marshaler,
		OverwriteSaramaConfig: saramaConfig,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("create kafka publisher: %w", err)
	}
	return publisher, nil
}

func NewSubscriber(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (*watermillkafka.Subscriber, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	saramaConfig := watermillkafka.DefaultSaramaSubscriberConfig()
	saramaConfig.ClientID = cfg.ClientID
	saramaConfig.Consumer.Offsets.Initial = sarama.OffsetOldest
	saramaConfig.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategySticky()

	subscriber, err := watermillkafka.NewSubscriber(watermillkafka.SubscriberConfig{
		Brokers:               cfg.Brokers,
		ConsumerGroup:         cfg.ConsumerGroup,
		Unmarshaler:           watermillkafka.DefaultMarshaler{},
		OverwriteSaramaConfig: saramaConfig,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("create kafka subscriber: %w", err)
	}
	return subscriber, nil
}
