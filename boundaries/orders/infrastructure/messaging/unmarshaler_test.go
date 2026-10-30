package messaging_test

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/infrastructure/messaging"
	"nomnom/orders/pkg/testfixture"
)

// framed is a record value in the Schema Registry wire format.
var framed = testfixture.WireFormat(3, 1, []byte("payload"))

func record(headers map[string]string) *sarama.ConsumerMessage {
	r := &sarama.ConsumerMessage{Topic: "source.v1.stream", Partition: 2, Offset: 41, Value: framed}
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

func TestGuardedUnmarshalerIdentifiesEventByEventID(t *testing.T) {
	msg, err := messaging.GuardedUnmarshaler{}.Unmarshal(record(validHeaders()))

	require.NoError(t, err)
	assert.Equal(t, "event-1", msg.UUID)
	assert.Empty(t, msg.Metadata.Get(messaging.DeserializationErrorMetadata))
	assert.Equal(t, framed, []byte(msg.Payload), "the value stays framed for the DLQ")
}

func TestGuardedUnmarshalerMarksInvalidEnvelopeInsteadOfFailing(t *testing.T) {
	headers := validHeaders()
	headers[messaging.OccurredAtMetadata] = "yesterday"

	msg, err := messaging.GuardedUnmarshaler{}.Unmarshal(record(headers))

	require.NoError(t, err, "an Unmarshal error would stop the partition")
	assert.Contains(t, msg.Metadata.Get(messaging.DeserializationErrorMetadata), "occurred_at")
	assert.Equal(t, framed, []byte(msg.Payload))
}

func TestGuardedUnmarshalerNamesRecordWithoutHeaders(t *testing.T) {
	msg, err := messaging.GuardedUnmarshaler{}.Unmarshal(record(nil))

	require.NoError(t, err)
	assert.Equal(t, "source.v1.stream/2/41", msg.UUID)
	assert.NotEmpty(t, msg.Metadata.Get(messaging.DeserializationErrorMetadata))
}

// AC-977: a bare Protobuf value is marked instead of stopping the partition.
func TestGuardedUnmarshalerMarksValueWithoutWireFormat(t *testing.T) {
	for name, value := range map[string][]byte{
		"bare payload":       []byte("payload"),
		"other magic byte":   append([]byte{1, 0, 0, 0, 3, 0}, "payload"...),
		"truncated indexes":  {0, 0, 0, 0, 3, 0x02},
		"negative index":     {0, 0, 0, 0, 3, 0x02, 0x01},
		"shorter than frame": {0, 0, 0, 3},
	} {
		t.Run(name, func(t *testing.T) {
			// Debezium sets the Watermill UUID header on every record.
			headers := validHeaders()
			headers["_watermill_message_uuid"] = "event-1"
			r := record(headers)
			r.Value = value

			msg, err := messaging.GuardedUnmarshaler{}.Unmarshal(r)

			require.NoError(t, err)
			assert.Equal(t, "source.v1.stream/2/41", msg.UUID)
			assert.NotEmpty(t, msg.Metadata.Get(messaging.DeserializationErrorMetadata))
			assert.Equal(t, value, []byte(msg.Payload))
			assert.Empty(t, msg.Metadata.Get("_watermill_message_uuid"), "the dead letter must be publishable")
		})
	}
}
