package messaging_test

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/messaging"
)

func record(headers map[string]string) *sarama.ConsumerMessage {
	r := &sarama.ConsumerMessage{Topic: "source.v1.stream", Partition: 2, Offset: 41, Value: []byte("payload")}
	for key, value := range headers {
		r.Headers = append(r.Headers, &sarama.RecordHeader{Key: []byte(key), Value: []byte(value)})
	}
	return r
}

func validHeaders() map[string]string {
	return map[string]string{
		messaging.EventIDMetadata:     "event-1",
		messaging.EventTypeMetadata:   "nomnom.restaurants.v1.RestaurantOrderReady",
		messaging.OccurredAtMetadata:  "2026-10-25T13:00:00Z",
		messaging.AggregateIDMetadata: "kitchen-1",
	}
}

func TestGuardedUnmarshalerUsesEventIDWithoutWatermillUUID(t *testing.T) {
	msg, err := messaging.GuardedUnmarshaler{}.Unmarshal(record(validHeaders()))

	require.NoError(t, err)
	assert.Equal(t, "event-1", msg.UUID)
	assert.Empty(t, msg.Metadata.Get(messaging.DeserializationErrorMetadata))
	assert.Equal(t, []byte("payload"), []byte(msg.Payload))
}

func TestGuardedUnmarshalerMarksInvalidEnvelopeInsteadOfFailing(t *testing.T) {
	headers := validHeaders()
	headers[messaging.OccurredAtMetadata] = "yesterday"

	msg, err := messaging.GuardedUnmarshaler{}.Unmarshal(record(headers))

	require.NoError(t, err, "an Unmarshal error would stop the partition")
	assert.Contains(t, msg.Metadata.Get(messaging.DeserializationErrorMetadata), "occurred_at")
	assert.Equal(t, []byte("payload"), []byte(msg.Payload))
}

func TestGuardedUnmarshalerNamesRecordWithoutHeaders(t *testing.T) {
	msg, err := messaging.GuardedUnmarshaler{}.Unmarshal(record(nil))

	require.NoError(t, err)
	assert.Equal(t, "source.v1.stream/2/41", msg.UUID)
	assert.NotEmpty(t, msg.Metadata.Get(messaging.DeserializationErrorMetadata))
}
