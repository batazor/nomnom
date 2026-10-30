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
// A valid Integration Event is identified by its event_id: publishers in other
// languages do not set the Watermill UUID header. An invalid record (broken
// envelope, value not in the Schema Registry wire format) has no trusted
// event_id and is identified by its record coordinates.
type GuardedUnmarshaler struct{}

var _ watermillkafka.Unmarshaler = GuardedUnmarshaler{}

func (GuardedUnmarshaler) Unmarshal(record *sarama.ConsumerMessage) (*message.Message, error) {
	msg, err := watermillkafka.DefaultMarshaler{}.Unmarshal(record)
	if err != nil {
		return invalid(record, err), nil
	}
	if _, err := EnvelopeFromMessage(msg); err != nil {
		return invalid(record, err), nil
	}
	// The value stays framed: a dead letter keeps the record as it was, so
	// redrive republishes it unchanged.
	if _, err := parseWireFrame(msg.Payload); err != nil {
		return invalid(record, err), nil
	}
	msg.UUID = msg.Metadata.Get(EventIDMetadata)
	return msg, nil
}

func invalid(record *sarama.ConsumerMessage, err error) *message.Message {
	msg := rawMessage(record)
	msg.Metadata.Set(DeserializationErrorMetadata, err.Error())
	return msg
}

func rawMessage(record *sarama.ConsumerMessage) *message.Message {
	msg := message.NewMessage(recordID(record), record.Value)
	for _, header := range record.Headers {
		// The UUID header of Watermill is not metadata: the dead letter of the
		// record could not be published with it.
		if header != nil && string(header.Key) != watermillkafka.UUIDHeaderKey {
			msg.Metadata.Set(string(header.Key), string(header.Value))
		}
	}
	return msg
}

func recordID(record *sarama.ConsumerMessage) string {
	return fmt.Sprintf("%s/%d/%d", record.Topic, record.Partition, record.Offset)
}
