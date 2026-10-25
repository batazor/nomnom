package messaging

import (
	"context"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "nomnom/orders/pkg/messaging"

// telemetry is the consumer instrumentation of ADR 28 on the global OpenTelemetry
// providers. The span continues the trace context from Kafka headers. Metric
// names follow ops/grafana/inventory (section messaging); attributes are
// handler_name, event_type, stream and error_class (only on failure), never
// event_id or aggregate_id. Consumer lag is measured broker-side.
type telemetry struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	duration   metric.Float64Histogram
	age        metric.Float64Histogram
	retries    metric.Int64Counter
	poison     metric.Int64Counter
	duplicates metric.Int64Counter
}

// Границы histograms в секундах: дефолтные границы OTel SDK (0, 5, 10, … 10000)
// рассчитаны на миллисекунды и сводят обработку за миллисекунды в первый bucket.
var (
	processDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
	messageAgeBuckets      = []float64{0.1, 0.5, 1, 5, 15, 30, 60, 300, 900, 3600}
)

func newTelemetry() (*telemetry, error) {
	meter := otel.Meter(instrumentationName)
	duration, err := meter.Float64Histogram("nomnom.messaging.process.duration",
		metric.WithUnit("s"), metric.WithDescription("Duration of processing one inbound Integration Event, retries included."),
		metric.WithExplicitBucketBoundaries(processDurationBuckets...))
	if err != nil {
		return nil, err
	}
	age, err := meter.Float64Histogram("nomnom.messaging.message.age",
		metric.WithUnit("s"), metric.WithDescription("now − occurred_at when the consumer receives the event."),
		metric.WithExplicitBucketBoundaries(messageAgeBuckets...))
	if err != nil {
		return nil, err
	}
	retries, err := meter.Int64Counter("nomnom.messaging.retry.attempts",
		metric.WithDescription("In-process retries of transient failures."))
	if err != nil {
		return nil, err
	}
	poison, err := meter.Int64Counter("nomnom.messaging.poison.messages",
		metric.WithDescription("Messages sent to <stream>.dlq."))
	if err != nil {
		return nil, err
	}
	duplicates, err := meter.Int64Counter("nomnom.inbox.duplicates",
		metric.WithDescription("Redeliveries completed as Inbox duplicates."))
	if err != nil {
		return nil, err
	}
	return &telemetry{
		tracer:     otel.Tracer(instrumentationName),
		propagator: propagation.TraceContext{},
		duration:   duration,
		age:        age,
		retries:    retries,
		poison:     poison,
		duplicates: duplicates,
	}, nil
}

// observation collects per-message facts for the metrics recorded by middleware.
type observation struct {
	errorClass Class
	duplicate  bool
	attempts   int
}

type observationKey struct{}

// MarkDuplicate records that the handler completed the message as an Inbox
// duplicate. Slice adapters call it; outside the consumer it is a no-op.
func MarkDuplicate(ctx context.Context) {
	if o, ok := ctx.Value(observationKey{}).(*observation); ok {
		o.duplicate = true
	}
}

func observe(ctx context.Context, class Class) {
	if o, ok := ctx.Value(observationKey{}).(*observation); ok {
		o.errorClass = class
	}
}

func baseAttributes(msg *message.Message) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("nomnom.messaging.handler", message.HandlerNameFromCtx(msg.Context())),
		attribute.String("nomnom.event.type", msg.Metadata.Get(EventTypeMetadata)),
		semconv.MessagingDestinationNameKey.String(message.SubscribeTopicFromCtx(msg.Context())),
	}
}

func (t *telemetry) middleware(next message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		attrs := baseAttributes(msg)
		o := &observation{}
		ctx := t.propagator.Extract(msg.Context(), propagation.MapCarrier(msg.Metadata))
		ctx = context.WithValue(ctx, observationKey{}, o)
		ctx, span := t.tracer.Start(ctx, message.SubscribeTopicFromCtx(msg.Context())+" process",
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(append(attrs,
				attribute.String("messaging.system", "kafka"),
				attribute.String("messaging.message.id", msg.UUID),
			)...))
		defer span.End()
		msg.SetContext(ctx)

		if envelope, err := EnvelopeFromMessage(msg); err == nil {
			t.age.Record(ctx, time.Since(envelope.OccurredAt).Seconds(), metric.WithAttributes(attrs...))
		}

		started := time.Now()
		produced, err := next(msg)

		if err != nil {
			o.errorClass = Classify(err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		recorded := attrs
		if o.errorClass != "" {
			recorded = append(recorded, attribute.String("nomnom.error.class", string(o.errorClass)))
		}
		t.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(recorded...))
		if o.duplicate {
			t.duplicates.Add(ctx, 1, metric.WithAttributes(attrs...))
		}
		return produced, err
	}
}

// countAttempts sits right inside Retry: every attempt after the first one of a
// delivery is a retry.
func (t *telemetry) countAttempts(next message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		if o, ok := msg.Context().Value(observationKey{}).(*observation); ok {
			o.attempts++
			if o.attempts > 1 {
				t.retries.Add(msg.Context(), 1, metric.WithAttributes(baseAttributes(msg)...))
			}
		}
		return next(msg)
	}
}

func (t *telemetry) poisoned(msg *message.Message, class Class) {
	observe(msg.Context(), class)
	t.poison.Add(msg.Context(), 1, metric.WithAttributes(append(baseAttributes(msg),
		attribute.String("nomnom.error.class", string(class)))...))
}
