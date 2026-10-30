package messaging_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"nomnom/orders/infrastructure/messaging"
	"nomnom/orders/pkg/testfixture"
)

const topic = "source.v1.stream"

// schemaID is the schema of the records the tests publish; stringValue is the
// event_type of wrapperspb.StringValue events.
const (
	schemaID    = uint32(7)
	stringValue = "google.protobuf.StringValue"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

type harness struct {
	pubSub *gochannel.GoChannel
	poison <-chan *message.Message
}

// start runs a consumer router with one stream handled by fn on gochannel.
// Publish blocks until the message is acked, so a returned Publish proves ack.
func start(t *testing.T, schemas messaging.SchemaSubjects, fn func(context.Context, *wrapperspb.StringValue) error) harness {
	t.Helper()
	pubSub := gochannel.NewGoChannel(gochannel.Config{BlockPublishUntilSubscriberAck: true}, watermill.NopLogger{})
	subscription, err := pubSub.Subscribe(context.Background(), messaging.DeadLetterTopic(topic))
	require.NoError(t, err)
	// DLQ publication also blocks until ack, so dead letters are acked as they arrive.
	poison := make(chan *message.Message, 10)
	go func() {
		for msg := range subscription {
			msg.Ack()
			poison <- msg
		}
	}()

	router, err := messaging.NewConsumerRouter(messaging.ConsumerConfig{
		ConsumerGroup: "orders",
		Retry:         messaging.RetryPolicy{MaxRetries: 2, InitialInterval: time.Millisecond, MaxInterval: time.Millisecond},
		Schemas:       schemas,
	}, pubSub, pubSub, []messaging.Stream{{
		Group:    "test",
		Topic:    topic,
		Handlers: []cqrs.GroupEventHandler{cqrs.NewGroupEventHandler(fn)},
	}}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = router.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = router.Close()
	})
	<-router.Running()
	return harness{pubSub: pubSub, poison: poison}
}

func event(t *testing.T, eventID string, payload proto.Message) *message.Message {
	t.Helper()
	msg, err := messaging.NewIntegrationEventMessage(messaging.Envelope{
		EventID:          eventID,
		OccurredAt:       occurredAt,
		AggregateType:    "source",
		AggregateID:      "aggregate-1",
		AggregateVersion: 1,
	}, payload)
	require.NoError(t, err)
	// The SchemaFraming SMT of the connector frames the Outbox payload.
	msg.Payload = testfixture.WireFormat(schemaID, 0, msg.Payload)
	return msg
}

func (h harness) publish(t *testing.T, msg *message.Message) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- h.pubSub.Publish(topic, msg) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("message was not acked")
	}
}

func (h harness) poisoned(t *testing.T) *message.Message {
	t.Helper()
	select {
	case msg := <-h.poison:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message in DLQ")
		return nil
	}
}

func (h harness) noPoison(t *testing.T) {
	t.Helper()
	select {
	case msg := <-h.poison:
		t.Fatalf("unexpected dead letter %s: %s", msg.UUID, msg.Metadata.Get(messaging.ReasonDeadLetteredMetadata))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDispatchesByEventTypeWithEnvelope(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	var got atomic.Value
	h := start(t, schemas, func(ctx context.Context, e *wrapperspb.StringValue) error {
		envelope, err := messaging.EnvelopeFromMessage(cqrs.OriginalMessageFromCtx(ctx))
		require.NoError(t, err)
		got.Store([2]string{envelope.EventID, e.GetValue()})
		assert.Equal(t, occurredAt, envelope.OccurredAt)
		assert.Equal(t, uint64(1), envelope.AggregateVersion)
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("hello")))

	assert.Equal(t, [2]string{"event-1", "hello"}, got.Load())
	h.noPoison(t)
}

func TestTransientErrorIsRetried(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	var calls atomic.Int32
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		if calls.Add(1) < 3 {
			return errors.New("database unavailable")
		}
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	assert.Equal(t, int32(3), calls.Load())
	h.noPoison(t)
}

// An exhausted transient failure is nacked, not dead-lettered: the partition
// waits and the message is redelivered until the dependency recovers.
func TestExhaustedTransientErrorIsNackedForRedelivery(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	var calls atomic.Int32
	recovered := make(chan struct{})
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		// MaxRetries 2: three attempts per delivery; the second delivery succeeds.
		if calls.Add(1) <= 3 {
			return errors.New("database unavailable")
		}
		close(recovered)
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	<-recovered
	assert.Equal(t, int32(4), calls.Load())
	h.noPoison(t)
}

func TestDeserializationErrorGoesToDLQBeforeHandler(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	var calls atomic.Int32
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return nil
	})
	msg := event(t, "event-1", wrapperspb.String("x"))
	msg.Metadata.Set(messaging.DeserializationErrorMetadata, "envelope event_id is required")

	h.publish(t, msg)

	letter := h.poisoned(t)
	assert.Equal(t, "envelope event_id is required", letter.Metadata.Get(messaging.ReasonDeadLetteredMetadata))
	assert.Equal(t, "orders", letter.Metadata.Get(messaging.GroupDeadLetteredMetadata))
	assert.Zero(t, calls.Load())
}

func TestPanicIsRecoveredAndRetried(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	var calls atomic.Int32
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	assert.Equal(t, int32(2), calls.Load())
	h.noPoison(t)
}

func TestPermanentErrorGoesToDLQWithoutRetry(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	var calls atomic.Int32
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return messaging.Permanent(errors.New("order not found"))
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	poisoned := h.poisoned(t)
	assert.Equal(t, "event-1", poisoned.UUID)
	assert.Equal(t, topic, poisoned.Metadata.Get(messaging.TopicDeadLetteredMetadata))
	assert.Equal(t, int32(1), calls.Load())
}

func TestBusinessErrorIsAckedWithoutRetry(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	var calls atomic.Int32
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return messaging.Business(errors.New("invalid transition"))
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	assert.Equal(t, int32(1), calls.Load())
	h.noPoison(t)
}

func TestUnknownEventTypeGoesToDLQ(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{"google.protobuf.Int64Value"}, nil)
	var calls atomic.Int32
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.Int64(1)))

	assert.Equal(t, "event-1", h.poisoned(t).UUID)
	assert.Zero(t, calls.Load())
}

func TestUndecodablePayloadGoesToDLQ(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error { return nil })
	msg := event(t, "event-1", wrapperspb.String("x"))
	msg.Payload = testfixture.WireFormat(schemaID, 0, []byte{0xff, 0xff, 0xff})

	h.publish(t, msg)

	assert.Equal(t, "event-1", h.poisoned(t).UUID)
}

func TestMissingEnvelopeGoesToDLQ(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	h := start(t, schemas, func(ctx context.Context, _ *wrapperspb.StringValue) error {
		_, err := messaging.EnvelopeFromMessage(cqrs.OriginalMessageFromCtx(ctx))
		return err
	})
	msg := event(t, "event-1", wrapperspb.String("x"))
	msg.Metadata.Set(messaging.OccurredAtMetadata, "")

	h.publish(t, msg)

	assert.Equal(t, "event-1", h.poisoned(t).UUID)
}

// AC-977: a value that is not in the wire format is a deserialization error.
func TestValueWithoutWireFormatGoesToDLQBeforeHandler(t *testing.T) {
	var calls atomic.Int32
	schemas := NewMockSchemaSubjects(t)
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return nil
	})
	msg := event(t, "event-1", wrapperspb.String("x"))
	msg.Payload, _ = proto.Marshal(wrapperspb.String("x"))

	h.publish(t, msg)

	assert.Contains(t, h.poisoned(t).Metadata.Get(messaging.ReasonDeadLetteredMetadata), "wire format")
	assert.Zero(t, calls.Load())
}

// AC-978: the schema of another event_type is a deserialization error.
func TestSchemaOfAnotherEventTypeGoesToDLQ(t *testing.T) {
	var calls atomic.Int32
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{"google.protobuf.Int64Value"}, nil)
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	letter := h.poisoned(t)
	assert.Equal(t, `schema 7 is not a schema of event_type "google.protobuf.StringValue"`,
		letter.Metadata.Get(messaging.ReasonDeadLetteredMetadata))
	assert.Zero(t, calls.Load())
}

func TestUnknownSchemaIDGoesToDLQ(t *testing.T) {
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return(nil, messaging.ErrUnknownSchema)
	h := start(t, schemas, func(context.Context, *wrapperspb.StringValue) error { return nil })

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	assert.Equal(t, "event-1", h.poisoned(t).UUID)
}

// AC-979: an unavailable Registry nacks the record; it is redelivered, not
// dead-lettered, and handled once the Registry answers.
func TestUnavailableSchemaRegistryIsRedelivered(t *testing.T) {
	handled := make(chan string, 1)
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return(nil, errors.New("connection refused")).Once()
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil).Once()
	h := start(t, schemas, func(_ context.Context, e *wrapperspb.StringValue) error {
		handled <- e.GetValue()
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	assert.Equal(t, "x", <-handled)
	h.noPoison(t)
}

func TestRouterRequiresSchemaRegistry(t *testing.T) {
	pubSub := gochannel.NewGoChannel(gochannel.Config{}, watermill.NopLogger{})

	_, err := messaging.NewConsumerRouter(messaging.ConsumerConfig{ConsumerGroup: "orders"}, pubSub, pubSub, nil, nil)

	require.ErrorContains(t, err, "schema registry")
}

func TestMarshalIsRefused(t *testing.T) {
	_, err := messaging.EnvelopeMarshaler{}.Marshal(wrapperspb.String("x"))

	require.ErrorIs(t, err, messaging.ErrPublishOutsideOutbox)
}

func TestClassify(t *testing.T) {
	cause := errors.New("cause")
	assert.Equal(t, messaging.ClassTransient, messaging.Classify(cause))
	assert.Equal(t, messaging.ClassPermanent, messaging.Classify(messaging.Permanent(cause)))
	assert.Equal(t, messaging.ClassBusiness, messaging.Classify(messaging.Business(cause)))
	assert.ErrorIs(t, messaging.Permanent(cause), cause)
}
