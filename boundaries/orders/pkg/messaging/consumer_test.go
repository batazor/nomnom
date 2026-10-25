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
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"nomnom/orders/pkg/messaging"
)

const (
	topic       = "source.v1.stream"
	poisonTopic = "orders.v1.consumer-poison"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

type harness struct {
	pubSub *gochannel.GoChannel
	poison <-chan *message.Message
}

// start runs a consumer router with one stream handled by fn on gochannel.
// Publish blocks until the message is acked, so a returned Publish proves ack.
func start(t *testing.T, fn func(context.Context, *wrapperspb.StringValue) error) harness {
	t.Helper()
	pubSub := gochannel.NewGoChannel(gochannel.Config{BlockPublishUntilSubscriberAck: true}, watermill.NopLogger{})
	subscription, err := pubSub.Subscribe(context.Background(), poisonTopic)
	require.NoError(t, err)
	// Poison publication also blocks until ack, so poison is acked as it arrives.
	poison := make(chan *message.Message, 10)
	go func() {
		for msg := range subscription {
			msg.Ack()
			poison <- msg
		}
	}()

	router, err := messaging.NewConsumerRouter(messaging.ConsumerConfig{
		PoisonTopic: poisonTopic,
		Retry:       messaging.RetryPolicy{MaxRetries: 2, InitialInterval: time.Millisecond, MaxInterval: time.Millisecond},
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
		t.Fatal("no message in poison topic")
		return nil
	}
}

func (h harness) noPoison(t *testing.T) {
	t.Helper()
	select {
	case msg := <-h.poison:
		t.Fatalf("unexpected poison message %s: %s", msg.UUID, msg.Metadata.Get("reason_poisoned"))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDispatchesByEventTypeWithEnvelope(t *testing.T) {
	var got atomic.Value
	h := start(t, func(ctx context.Context, e *wrapperspb.StringValue) error {
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
	var calls atomic.Int32
	h := start(t, func(context.Context, *wrapperspb.StringValue) error {
		if calls.Add(1) < 3 {
			return errors.New("database unavailable")
		}
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	assert.Equal(t, int32(3), calls.Load())
	h.noPoison(t)
}

func TestExhaustedTransientErrorGoesToPoison(t *testing.T) {
	var calls atomic.Int32
	h := start(t, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return errors.New("database unavailable")
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	assert.Equal(t, "event-1", h.poisoned(t).UUID)
	assert.Equal(t, int32(3), calls.Load())
}

func TestPermanentErrorGoesToPoisonWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	h := start(t, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return messaging.Permanent(errors.New("order not found"))
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	poisoned := h.poisoned(t)
	assert.Equal(t, "event-1", poisoned.UUID)
	assert.Equal(t, topic, poisoned.Metadata.Get("topic_poisoned"))
	assert.Equal(t, int32(1), calls.Load())
}

func TestBusinessErrorIsAckedWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	h := start(t, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return messaging.Business(errors.New("invalid transition"))
	})

	h.publish(t, event(t, "event-1", wrapperspb.String("x")))

	assert.Equal(t, int32(1), calls.Load())
	h.noPoison(t)
}

func TestUnknownEventTypeGoesToPoison(t *testing.T) {
	var calls atomic.Int32
	h := start(t, func(context.Context, *wrapperspb.StringValue) error {
		calls.Add(1)
		return nil
	})

	h.publish(t, event(t, "event-1", wrapperspb.Int64(1)))

	assert.Equal(t, "event-1", h.poisoned(t).UUID)
	assert.Zero(t, calls.Load())
}

func TestUndecodablePayloadGoesToPoison(t *testing.T) {
	h := start(t, func(context.Context, *wrapperspb.StringValue) error { return nil })
	msg := event(t, "event-1", wrapperspb.String("x"))
	msg.Payload = []byte{0xff, 0xff, 0xff}

	h.publish(t, msg)

	assert.Equal(t, "event-1", h.poisoned(t).UUID)
}

func TestMissingEnvelopeGoesToPoison(t *testing.T) {
	h := start(t, func(ctx context.Context, _ *wrapperspb.StringValue) error {
		_, err := messaging.EnvelopeFromMessage(cqrs.OriginalMessageFromCtx(ctx))
		return err
	})
	msg := event(t, "event-1", wrapperspb.String("x"))
	msg.Metadata.Set(messaging.OccurredAtMetadata, "")

	h.publish(t, msg)

	assert.Equal(t, "event-1", h.poisoned(t).UUID)
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
