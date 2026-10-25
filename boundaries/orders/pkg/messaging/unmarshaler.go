package messaging

import (
	"fmt"

	"github.com/IBM/sarama"
	watermillkafka "github.com/ThreeDotsLabs/watermill-kafka/v3/pkg/kafka"
	"github.com/ThreeDotsLabs/watermill/message"
)

// DeserializationErrorMetadata marks a Kafka record that could not be turned
// into an Integration Event; the deserialization guard sends it to the DLQ.
const DeserializationErrorMetadata = "deserialization_error"

// GuardedUnmarshaler never returns an error. An Unmarshal error of the sarama
// subscriber is fail-fast: the record is not acked, its offset is not committed
// and the partition stops. An invalid record becomes a message with the raw
// payload and deserialization_error metadata instead (ADR 28).
//
// Publishers in other languages do not set the Watermill UUID header
// (_watermill_message_uuid, reserved by watermill-kafka), so the message UUID
// falls back to event_id and then to the record coordinates.
type GuardedUnmarshaler struct {
	// Next defaults to watermillkafka.DefaultMarshaler.
	Next watermillkafka.Unmarshaler
}

var _ watermillkafka.Unmarshaler = GuardedUnmarshaler{}

func (u GuardedUnmarshaler) Unmarshal(record *sarama.ConsumerMessage) (*message.Message, error) {
	next := u.Next
	if next == nil {
		next = watermillkafka.DefaultMarshaler{}
	}

	msg, err := next.Unmarshal(record)
	if err != nil {
		msg = rawMessage(record)
		msg.Metadata.Set(DeserializationErrorMetadata, err.Error())
		return msg, nil
	}
	if msg.UUID == "" {
		msg.UUID = msg.Metadata.Get(EventIDMetadata)
	}
	if msg.UUID == "" {
		msg.UUID = recordID(record)
	}
	if _, err := EnvelopeFromMessage(msg); err != nil {
		msg.Metadata.Set(DeserializationErrorMetadata, err.Error())
	}
	return msg, nil
}

func rawMessage(record *sarama.ConsumerMessage) *message.Message {
	msg := message.NewMessage(recordID(record), record.Value)
	for _, header := range record.Headers {
		if header != nil {
			msg.Metadata.Set(string(header.Key), string(header.Value))
		}
	}
	return msg
}

func recordID(record *sarama.ConsumerMessage) string {
	return fmt.Sprintf("%s/%d/%d", record.Topic, record.Partition, record.Offset)
}
