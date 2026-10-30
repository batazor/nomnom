package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/infrastructure/messaging"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/testfixture"
)

const stream = "restaurants.v1.restaurant-order-events"

// A group resolves only its own dead letters: discard and redrive move its
// <group>.dlq offset, the redriven record is the original event without the
// dead letter metadata, and the dead letters of another group stay.
func TestDeadLettersAgainstKafka(t *testing.T) {
	ctx := context.Background()
	kafka, err := testfixture.StartKafka(ctx)
	require.NoError(t, err)
	t.Cleanup(kafka.Close)

	admin, err := sarama.NewClusterAdmin(kafka.Brokers, sarama.NewConfig())
	require.NoError(t, err)
	for _, topic := range []string{stream, messaging.DeadLetterTopic(stream)} {
		require.NoError(t, admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false))
	}
	require.NoError(t, admin.Close())

	deadLetter(t, kafka.Brokers, "orders", "event-discarded", "unknown event_type")
	deadLetter(t, kafka.Brokers, "restaurants", "event-foreign", "permanent")
	deadLetter(t, kafka.Brokers, "orders", "event-redriven", "permanent")

	orders := deadLetters(t, kafka.Brokers, "orders")
	listed, err := orders.List(ctx, stream)
	require.NoError(t, err)
	assert.Equal(t, []string{"event-discarded", "event-redriven"}, eventIDs(listed))
	assert.Equal(t, "unknown event_type", listed[0].Reason())

	var seen []string
	discarded, err := orders.Discard(ctx, stream, messaging.Through{Partition: 0, Offset: listed[0].Offset},
		func(letter messaging.DeadLetter) { seen = append(seen, letter.Headers[messaging.EventIDMetadata]) })
	require.NoError(t, err)
	assert.Equal(t, 1, discarded)
	assert.Equal(t, []string{"event-discarded"}, seen)

	redriven, err := orders.Redrive(ctx, stream, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, redriven)

	record := firstRecord(t, kafka.Brokers, stream)
	headers := map[string]string{}
	for _, header := range record.Headers {
		headers[string(header.Key)] = string(header.Value)
	}
	assert.Equal(t, "order-1", string(record.Key))
	assert.Equal(t, []byte("payload"), record.Value)
	assert.Equal(t, "event-redriven", headers[messaging.EventIDMetadata])
	assert.Equal(t, "nomnom.restaurants.v1.RestaurantOrderAccepted", headers[messaging.EventTypeMetadata])
	assert.Equal(t, messaging.DeadLetterTopic(stream)+"/0@2", headers[messaging.RedrivenFromMetadata])
	for _, key := range []string{messaging.ReasonDeadLetteredMetadata, messaging.TopicDeadLetteredMetadata, messaging.GroupDeadLetteredMetadata} {
		assert.NotContains(t, headers, key)
	}

	remaining, err := orders.List(ctx, stream)
	require.NoError(t, err)
	assert.Empty(t, remaining, "resolved dead letters are not listed again")
	foreign, err := deadLetters(t, kafka.Brokers, "restaurants").List(ctx, stream)
	require.NoError(t, err)
	assert.Equal(t, []string{"event-foreign"}, eventIDs(foreign))

	missing, err := orders.List(ctx, "delivery.v1.delivery-events")
	require.NoError(t, err)
	assert.Empty(t, missing, "a DLQ nobody wrote to has no dead letters")
}

func deadLetters(t *testing.T, brokers []string, group string) *messaging.DeadLetters {
	t.Helper()
	letters, err := messaging.NewDeadLetters(config.KafkaConfig{
		Brokers: brokers, ClientID: "orders-admin-test", ConsumerGroup: group, InitialOffset: config.KafkaOffsetOldest,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = letters.Close() })
	return letters
}

// deadLetter writes a record as the consumer DLQ does: envelope headers of the
// event and the dead letter metadata of the group.
func deadLetter(t *testing.T, brokers []string, group, eventID, reason string) {
	t.Helper()
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer(brokers, cfg)
	require.NoError(t, err)
	defer func() { _ = producer.Close() }()
	headers := map[string]string{
		messaging.EventIDMetadata:            eventID,
		messaging.EventTypeMetadata:          "nomnom.restaurants.v1.RestaurantOrderAccepted",
		messaging.AggregateIDMetadata:        "order-1",
		messaging.OccurredAtMetadata:         time.Date(2026, time.October, 26, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		messaging.ReasonDeadLetteredMetadata: reason,
		messaging.TopicDeadLetteredMetadata:  stream,
		messaging.GroupDeadLetteredMetadata:  group,
		"_watermill_message_uuid":            eventID,
	}
	record := &sarama.ProducerMessage{
		Topic: messaging.DeadLetterTopic(stream), Key: sarama.StringEncoder("order-1"), Value: sarama.ByteEncoder("payload"),
	}
	for key, value := range headers {
		record.Headers = append(record.Headers, sarama.RecordHeader{Key: []byte(key), Value: []byte(value)})
	}
	_, _, err = producer.SendMessage(record)
	require.NoError(t, err)
}

func firstRecord(t *testing.T, brokers []string, topic string) *sarama.ConsumerMessage {
	t.Helper()
	consumer, err := sarama.NewConsumer(brokers, sarama.NewConfig())
	require.NoError(t, err)
	defer func() { _ = consumer.Close() }()
	partition, err := consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
	require.NoError(t, err)
	defer func() { _ = partition.Close() }()
	select {
	case record := <-partition.Messages():
		return record
	case <-time.After(30 * time.Second):
		require.FailNow(t, "no redriven record on "+topic)
		return nil
	}
}

func eventIDs(letters []messaging.DeadLetter) []string {
	ids := make([]string, 0, len(letters))
	for _, letter := range letters {
		ids = append(ids, letter.Headers[messaging.EventIDMetadata])
	}
	return ids
}
