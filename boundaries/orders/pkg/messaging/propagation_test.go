package messaging_test

import (
	"context"
	"testing"

	"github.com/IBM/sarama"
	watermillkafka "github.com/ThreeDotsLabs/watermill-kafka/v3/pkg/kafka"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"nomnom/orders/pkg/messaging"
)

// ADR 37: the trace context of the operation that writes an event to the
// Outbox is stored with the row; Debezium sends it as the traceparent header
// unchanged, and the consumer extracts the write span as the parent of
// processing.
func TestTraceContextTravelsFromOutboxWriteToConsumer(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	// Write side: the use case span is active when the Outbox record is written.
	writeCtx, writeSpan := provider.Tracer("test").Start(context.Background(), "usecase place_order")
	msg, err := messaging.NewEventMessage("event-1", "order-1", []byte("payload"))
	require.NoError(t, err)
	messaging.InjectTraceContext(writeCtx, msg)
	writeSpan.End()
	traceparent := msg.Metadata.Get(messaging.TraceParentMetadata)
	assert.Equal(t, "00-"+writeSpan.SpanContext().TraceID().String()+"-"+writeSpan.SpanContext().SpanID().String()+"-01",
		traceparent)

	// Debezium record: Event Envelope headers from the Outbox row columns.
	headers := []*sarama.RecordHeader{
		{Key: []byte("_watermill_message_uuid"), Value: []byte("event-1")},
		{Key: []byte(messaging.EventIDMetadata), Value: []byte("event-1")},
		{Key: []byte(messaging.AggregateIDMetadata), Value: []byte("order-1")},
		{Key: []byte(messaging.TraceParentMetadata), Value: []byte(traceparent)},
	}
	received, err := watermillkafka.DefaultMarshaler{}.Unmarshal(&sarama.ConsumerMessage{
		Key: []byte("order-1"), Headers: headers, Value: []byte("payload"),
	})
	require.NoError(t, err)
	parent := trace.SpanContextFromContext(messaging.ExtractTraceContext(context.Background(), received))
	assert.True(t, parent.IsRemote())
	assert.Equal(t, writeSpan.SpanContext().TraceID(), parent.TraceID())
	assert.Equal(t, writeSpan.SpanContext().SpanID(), parent.SpanID())
	assert.Equal(t, "event-1", received.UUID)
	assert.Equal(t, "order-1", received.Metadata.Get(messaging.AggregateIDMetadata), "envelope metadata survives")
}

// An Outbox row without trace context has an empty traceparent header:
// processing starts a new trace.
func TestEmptyTraceparentStartsNewTrace(t *testing.T) {
	msg := message.NewMessage("event-1", nil)
	msg.Metadata.Set(messaging.TraceParentMetadata, "")
	assert.False(t, trace.SpanContextFromContext(messaging.ExtractTraceContext(context.Background(), msg)).IsValid())
}
