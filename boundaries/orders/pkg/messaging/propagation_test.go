package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	"github.com/ThreeDotsLabs/watermill"
	watermillkafka "github.com/ThreeDotsLabs/watermill-kafka/v3/pkg/kafka"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/dnwe/otelsarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"nomnom/orders/pkg/messaging"
)

// kafkaPublisher publishes like watermill-kafka: Watermill metadata → Kafka
// headers through the default marshaler, then the otelsarama-wrapped producer.
type kafkaPublisher struct {
	producer sarama.SyncProducer
	sent     chan *sarama.ProducerMessage
}

func (p kafkaPublisher) Publish(topic string, messages ...*message.Message) error {
	for _, msg := range messages {
		record, err := watermillkafka.DefaultMarshaler{}.Marshal(topic, msg)
		if err != nil {
			return err
		}
		if _, _, err := p.producer.SendMessage(record); err != nil {
			return err
		}
		p.sent <- record
	}
	return nil
}

func (kafkaPublisher) Close() error { return nil }

// The trace context of the operation that writes an event to the Outbox reaches
// the Kafka headers through the Forwarder envelope; the producer span continues
// it, and the consumer extracts the producer span as the parent of processing.
func TestTraceContextTravelsFromOutboxWriteToKafkaHeaders(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	// Write side: the use case span is active when the Outbox record is written.
	writeCtx, writeSpan := provider.Tracer("test").Start(context.Background(), "usecase place_order")
	msg, err := messaging.NewEventMessage("event-1", "order-1", []byte("payload"))
	require.NoError(t, err)
	messaging.InjectTraceContext(writeCtx, msg)
	writeSpan.End()
	assert.Equal(t, "00-"+writeSpan.SpanContext().TraceID().String()+"-"+writeSpan.SpanContext().SpanID().String()+"-01",
		msg.Metadata.Get(messaging.TraceParentMetadata))

	// Outbox → Forwarder envelope over an in-memory pub/sub instead of PostgreSQL.
	outbox := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = outbox.Close() })
	require.NoError(t, forwarder.NewPublisher(outbox, forwarder.PublisherConfig{ForwarderTopic: "orders_outbox"}).
		Publish("nomnom.orders.v1.events", msg))

	saramaConfig := sarama.NewConfig()
	mock := mocks.NewSyncProducer(t, saramaConfig)
	mock.ExpectSendMessageAndSucceed()
	out := kafkaPublisher{
		producer: otelsarama.WrapSyncProducer(saramaConfig, mock,
			otelsarama.WithTracerProvider(provider), otelsarama.WithPropagators(propagation.TraceContext{})),
		sent: make(chan *sarama.ProducerMessage, 1),
	}
	fwd, err := forwarder.NewForwarder(outbox, out, watermill.NopLogger{}, forwarder.Config{ForwarderTopic: "orders_outbox"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = fwd.Run(ctx) }()

	var record *sarama.ProducerMessage
	select {
	case record = <-out.sent:
	case <-time.After(5 * time.Second):
		t.Fatal("forwarder did not publish the outbox record")
	}
	require.NoError(t, fwd.Close())

	var producer sdktrace.ReadOnlySpan
	for _, s := range spans.Ended() {
		if s.SpanKind() == trace.SpanKindProducer {
			producer = s
		}
	}
	require.NotNil(t, producer, "otelsarama producer span")
	assert.Equal(t, writeSpan.SpanContext().TraceID(), producer.SpanContext().TraceID())
	assert.Equal(t, writeSpan.SpanContext().SpanID(), producer.Parent().SpanID(), "producer span continues the write context")

	// Consumer side: headers → Watermill metadata → extracted remote parent.
	headers := make([]*sarama.RecordHeader, 0, len(record.Headers))
	for i := range record.Headers {
		headers = append(headers, &record.Headers[i])
	}
	payload, err := record.Value.Encode()
	require.NoError(t, err)
	received, err := watermillkafka.DefaultMarshaler{}.Unmarshal(&sarama.ConsumerMessage{Headers: headers, Value: payload})
	require.NoError(t, err)
	parent := trace.SpanContextFromContext(messaging.ExtractTraceContext(context.Background(), received))
	assert.True(t, parent.IsRemote())
	assert.Equal(t, producer.SpanContext().TraceID(), parent.TraceID())
	assert.Equal(t, producer.SpanContext().SpanID(), parent.SpanID())
	assert.Equal(t, "order-1", received.Metadata.Get(messaging.AggregateIDMetadata), "envelope metadata survives")
}

// Outside a span the metadata stays without traceparent: no fake context.
func TestInjectTraceContextOutsideSpanAddsNothing(t *testing.T) {
	msg := message.NewMessage("event-1", nil)
	messaging.InjectTraceContext(context.Background(), msg)
	assert.Empty(t, msg.Metadata.Get(messaging.TraceParentMetadata))
	assert.Empty(t, msg.Metadata.Get(messaging.TraceStateMetadata))
}

// A malformed traceparent is ignored: processing starts a new trace.
func TestExtractTraceContextIgnoresMalformedTraceparent(t *testing.T) {
	msg := message.NewMessage("event-1", nil)
	msg.Metadata.Set(messaging.TraceParentMetadata, "00-not-a-trace-01")
	assert.False(t, trace.SpanContextFromContext(messaging.ExtractTraceContext(context.Background(), msg)).IsValid())
}
