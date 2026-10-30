package messaging

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"github.com/ThreeDotsLabs/watermill/message"
	"google.golang.org/protobuf/proto"
)

// Event Envelope headers of an Integration Event (ADR 25, ADR 28). The payload
// is the Protobuf contract; event_type is its full message name, so publishers
// in any language produce the same routing key.
const (
	EventTypeMetadata        = "event_type"
	OccurredAtMetadata       = "occurred_at"
	AggregateTypeMetadata    = "aggregate_type"
	AggregateIDMetadata      = "aggregate_id"
	AggregateVersionMetadata = "aggregate_version"
	TraceParentMetadata      = "traceparent"
)

// Envelope is the transport part of an inbound Integration Event that Watermill
// handlers pass to application Commands.
type Envelope struct {
	EventID          string
	EventType        string
	OccurredAt       time.Time
	AggregateType    string
	AggregateID      string
	AggregateVersion uint64
}

func (e Envelope) validate() error {
	switch {
	case e.EventID == "":
		return fmt.Errorf("envelope %s is required", EventIDMetadata)
	case e.EventType == "":
		return fmt.Errorf("envelope %s is required", EventTypeMetadata)
	case e.OccurredAt.IsZero():
		return fmt.Errorf("envelope %s is required", OccurredAtMetadata)
	case e.AggregateID == "":
		return fmt.Errorf("envelope %s is required", AggregateIDMetadata)
	}
	return nil
}

// EnvelopeFromMessage reads and validates the envelope headers. Errors are permanent:
// redelivery of the same message cannot fix them.
func EnvelopeFromMessage(msg *message.Message) (Envelope, error) {
	envelope := Envelope{
		EventID:       msg.Metadata.Get(EventIDMetadata),
		EventType:     msg.Metadata.Get(EventTypeMetadata),
		AggregateType: msg.Metadata.Get(AggregateTypeMetadata),
		AggregateID:   msg.Metadata.Get(AggregateIDMetadata),
	}
	if raw := msg.Metadata.Get(OccurredAtMetadata); raw != "" {
		occurredAt, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return Envelope{}, Permanent(fmt.Errorf("parse %s: %w", OccurredAtMetadata, err))
		}
		envelope.OccurredAt = occurredAt
	}
	if raw := msg.Metadata.Get(AggregateVersionMetadata); raw != "" {
		version, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return Envelope{}, Permanent(fmt.Errorf("parse %s: %w", AggregateVersionMetadata, err))
		}
		envelope.AggregateVersion = version
	}
	if err := envelope.validate(); err != nil {
		return Envelope{}, Permanent(err)
	}
	return envelope, nil
}

// NewIntegrationEventMessage builds the Outbox message of an Integration Event:
// the payload is the bare Protobuf, the SchemaFraming SMT of the connector
// frames it in the wire format on delivery (FR-714).
func NewIntegrationEventMessage(envelope Envelope, event proto.Message) (*message.Message, error) {
	envelope.EventType = EventName(event)
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", envelope.EventType, err)
	}
	msg := message.NewMessage(envelope.EventID, payload)
	msg.Metadata.Set(EventIDMetadata, envelope.EventID)
	msg.Metadata.Set(EventTypeMetadata, envelope.EventType)
	msg.Metadata.Set(OccurredAtMetadata, envelope.OccurredAt.UTC().Format(time.RFC3339Nano))
	msg.Metadata.Set(AggregateTypeMetadata, envelope.AggregateType)
	msg.Metadata.Set(AggregateIDMetadata, envelope.AggregateID)
	msg.Metadata.Set(AggregateVersionMetadata, strconv.FormatUint(envelope.AggregateVersion, 10))
	return msg, nil
}

// EventName is the event_type of a Protobuf contract.
func EventName(event proto.Message) string {
	return string(event.ProtoReflect().Descriptor().FullName())
}

// ErrPublishOutsideOutbox is returned by EnvelopeMarshaler.Marshal: Integration
// Events leave orders only through the Outbox and Debezium (ADR 13, ADR 37).
var ErrPublishOutsideOutbox = errors.New("integration events are published only through the outbox")

// EnvelopeMarshaler is the cqrs.CommandEventMarshaler of inbound Integration
// Events: the name comes from the event_type header, the payload is Protobuf
// in the Schema Registry wire format.
// The default Watermill marshalers name events after Go structs and are not
// used, because events are published by contexts in other languages.
type EnvelopeMarshaler struct{}

var _ cqrs.CommandEventMarshaler = EnvelopeMarshaler{}

func (EnvelopeMarshaler) Marshal(any) (*message.Message, error) {
	return nil, ErrPublishOutsideOutbox
}

func (EnvelopeMarshaler) Unmarshal(msg *message.Message, v any) error {
	event, ok := v.(proto.Message)
	if !ok {
		return Permanent(fmt.Errorf("event %T is not a protobuf message", v))
	}
	frame, err := parseWireFrame(msg.Payload)
	if err != nil {
		return Permanent(fmt.Errorf("unmarshal %s: %w", EventName(event), err))
	}
	if err := proto.Unmarshal(frame.Payload, event); err != nil {
		return Permanent(fmt.Errorf("unmarshal %s: %w", EventName(event), err))
	}
	return nil
}

func (EnvelopeMarshaler) Name(v any) string {
	if event, ok := v.(proto.Message); ok {
		return EventName(event)
	}
	return fmt.Sprintf("%T", v)
}

func (EnvelopeMarshaler) NameFromMessage(msg *message.Message) string {
	return msg.Metadata.Get(EventTypeMetadata)
}
