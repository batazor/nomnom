// Package messaging owns the Watermill/Kafka transport conventions shared by
// orders publishers and consumers.
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
	EventIDMetadata = "event_id"
	// PartitionKeyMetadata is the order_id of orders events. The Kafka key is
	// aggregate_id (AggregateIDMetadata); for the Order aggregate both are equal.
	PartitionKeyMetadata = "order_id"
)

// NewEventMessage builds the transport representation required by the Inbox
// and ordering ADRs: UUID and event_id are identical, and order_id is the
// aggregate_id that keys the Kafka partition.
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
	msg.Metadata.Set(AggregateIDMetadata, orderID)
	return msg, nil
}

// NewDeadLetterPublisher publishes messages rejected by the consumer to the DLQ
// of their stream, keyed by aggregate_id when the record carried one.
func NewDeadLetterPublisher(cfg config.KafkaConfig, logger watermill.LoggerAdapter) (*watermillkafka.Publisher, error) {
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
		// otelsarama: producer spans and traceparent in Kafka headers.
		Tracer: watermillkafka.NewOTELSaramaTracer(),
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
	// Without committed offsets the group starts where KafkaConfig.InitialOffset
	// says: oldest replays the whole stream through the Inbox.
	saramaConfig.Consumer.Offsets.Initial = sarama.OffsetOldest
	if cfg.InitialOffset == config.KafkaOffsetNewest {
		saramaConfig.Consumer.Offsets.Initial = sarama.OffsetNewest
	}
	saramaConfig.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategySticky()

	subscriber, err := watermillkafka.NewSubscriber(watermillkafka.SubscriberConfig{
		Brokers:               cfg.Brokers,
		ConsumerGroup:         cfg.ConsumerGroup,
		Unmarshaler:           GuardedUnmarshaler{},
		OverwriteSaramaConfig: saramaConfig,
		Tracer:                watermillkafka.NewOTELSaramaTracer(),
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("create kafka subscriber: %w", err)
	}
	return subscriber, nil
}
