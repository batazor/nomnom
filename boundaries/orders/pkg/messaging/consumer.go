package messaging

import (
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"github.com/ThreeDotsLabs/watermill/components/metrics"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

// Stream is one stream topic of a source aggregate and the handler group that
// consumes it. One group shares one subscriber, so events of one aggregate are
// handled in the order of their partition (ADR 28).
type Stream struct {
	// Group is the stable handler group name.
	Group    string
	Topic    string
	Handlers []cqrs.GroupEventHandler
}

// DeadLetterTopic is the DLQ of a stream topic.
func DeadLetterTopic(topic string) string { return topic + ".dlq" }

// Dead-letter metadata, compatible with the Watermill PoisonQueue middleware.
const (
	ReasonDeadLetteredMetadata = "reason_poisoned"
	TopicDeadLetteredMetadata  = "topic_poisoned"
	GroupDeadLetteredMetadata  = "consumer_group_poisoned"
)

// RetryPolicy bounds the in-process redelivery of transient failures.
type RetryPolicy struct {
	MaxRetries      int
	InitialInterval time.Duration
	MaxInterval     time.Duration
}

var DefaultRetryPolicy = RetryPolicy{
	MaxRetries:      5,
	InitialInterval: 100 * time.Millisecond,
	MaxInterval:     5 * time.Second,
}

type ConsumerConfig struct {
	// ConsumerGroup is recorded on dead letters: several contexts may consume
	// one stream and share its DLQ.
	ConsumerGroup string
	Retry         RetryPolicy
	CloseTimeout  time.Duration
	// Prometheus принимает встроенные метрики Watermill (namespace watermill,
	// subsystem consumer); они уходят в OTLP через Prometheus bridge. nil — без них.
	Prometheus prometheus.Registerer
}

// Namespace и subsystem встроенных метрик Watermill consumer-роли: имена
// watermill_consumer_* не пересекаются с ролью outbox_forwarder.
const (
	WatermillMetricsNamespace = "watermill"
	ConsumerMetricsSubsystem  = "consumer"
	// ForwarderMetricsSubsystem — роль orders-forwarder (Outbox → Kafka).
	ForwarderMetricsSubsystem = "outbox_forwarder"
)

// NewConsumerRouter builds the Watermill router of the consumer process: one
// EventGroupProcessor handler group per stream and the middleware chain of
// ADR 28, outermost first:
//
//	Recoverer → OpenTelemetry/metrics → Watermill metrics → deserialization guard → Retry → dead letter → unknown event_type → handler
//
//	deserialization error, permanent, unknown → <stream>.dlq + ack
//	transient → Retry with backoff; exhausted → nack, the partition waits and Kafka redelivers
//	business → ack, logged
//	nil, including an Inbox duplicate → ack
func NewConsumerRouter(
	cfg ConsumerConfig,
	subscriber message.Subscriber,
	deadLetters message.Publisher,
	streams []Stream,
	logger watermill.LoggerAdapter,
) (*message.Router, error) {
	if logger == nil {
		logger = watermill.NopLogger{}
	}
	tel, err := newTelemetry()
	if err != nil {
		return nil, fmt.Errorf("create consumer telemetry: %w", err)
	}

	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: cfg.CloseTimeout}, logger)
	if err != nil {
		return nil, fmt.Errorf("create router: %w", err)
	}

	middlewares := []message.HandlerMiddleware{middleware.Recoverer, tel.middleware}
	if cfg.Prometheus != nil {
		builtin := metrics.NewPrometheusMetricsBuilder(cfg.Prometheus, WatermillMetricsNamespace, ConsumerMetricsSubsystem)
		router.AddSubscriberDecorators(builtin.DecorateSubscriber)
		if deadLetters, err = builtin.DecoratePublisher(deadLetters); err != nil {
			return nil, fmt.Errorf("decorate dead letter publisher: %w", err)
		}
		middlewares = append(middlewares, builtin.NewRouterMiddleware().Middleware)
	}

	dlq := deadLetterQueue{publisher: deadLetters, group: cfg.ConsumerGroup, telemetry: tel, logger: logger}
	retry := middleware.Retry{
		MaxRetries:      cfg.Retry.MaxRetries,
		InitialInterval: cfg.Retry.InitialInterval,
		MaxInterval:     cfg.Retry.MaxInterval,
		Multiplier:      2,
		ShouldRetry: func(params middleware.RetryParams) bool {
			return Classify(params.Err) == ClassTransient
		},
		Logger: logger,
	}

	marshaler := EnvelopeMarshaler{}
	topics := make(map[string]string, len(streams))
	known := make(map[string]map[string]bool, len(streams))
	for _, stream := range streams {
		topics[stream.Group] = stream.Topic
		names := make(map[string]bool, len(stream.Handlers))
		for _, handler := range stream.Handlers {
			names[marshaler.Name(handler.NewEvent())] = true
		}
		known[stream.Topic] = names
	}

	// The first middleware is the outermost one.
	router.AddMiddleware(append(middlewares,
		dlq.guard,
		retry.Middleware,
		tel.countAttempts,
		dlq.permanent,
		rejectUnknownEvents(known),
	)...)

	processor, err := cqrs.NewEventGroupProcessorWithConfig(router, cqrs.EventGroupProcessorConfig{
		GenerateSubscribeTopic: func(params cqrs.EventGroupProcessorGenerateSubscribeTopicParams) (string, error) {
			topic, ok := topics[params.EventGroupName]
			if !ok {
				return "", fmt.Errorf("no topic for handler group %s", params.EventGroupName)
			}
			return topic, nil
		},
		SubscriberConstructor: func(cqrs.EventGroupProcessorSubscriberConstructorParams) (message.Subscriber, error) {
			return subscriber, nil
		},
		OnHandle: func(params cqrs.EventGroupProcessorOnHandleParams) error {
			err := params.Handler.Handle(params.Message.Context(), params.Event)
			if Classify(err) == ClassBusiness {
				observe(params.Message.Context(), ClassBusiness)
				logger.Info("integration event refused by application, acked", watermill.LogFields{
					"event_id":   params.Message.Metadata.Get(EventIDMetadata),
					"event_type": params.EventName,
					"group":      params.GroupName,
					"err":        err,
				})
				return nil
			}
			return err
		},
		Marshaler: marshaler,
		Logger:    logger,
	})
	if err != nil {
		return nil, fmt.Errorf("create event group processor: %w", err)
	}
	for _, stream := range streams {
		if err := processor.AddHandlersGroup(stream.Group, stream.Handlers...); err != nil {
			return nil, fmt.Errorf("add handler group %s: %w", stream.Group, err)
		}
	}
	return router, nil
}

// deadLetterQueue publishes rejected messages to <stream>.dlq and acks them.
type deadLetterQueue struct {
	publisher message.Publisher
	group     string
	telemetry *telemetry
	logger    watermill.LoggerAdapter
}

// guard sends records the GuardedUnmarshaler could not decode to the DLQ
// before any handler sees them, so a broken record never blocks the partition.
func (q deadLetterQueue) guard(next message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		if reason := msg.Metadata.Get(DeserializationErrorMetadata); reason != "" {
			return nil, q.send(msg, ClassDeserialization, reason)
		}
		return next(msg)
	}
}

// permanent sends messages whose handler failed permanently to the DLQ;
// transient errors pass through to Retry.
func (q deadLetterQueue) permanent(next message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		produced, err := next(msg)
		if err == nil || Classify(err) != ClassPermanent {
			return produced, err
		}
		return nil, q.send(msg, ClassPermanent, err.Error())
	}
}

// send returns nil after the DLQ accepted the message, which acks it; a failed
// publication is returned, so the message is redelivered instead of lost.
func (q deadLetterQueue) send(msg *message.Message, class Class, detail string) error {
	topic := message.SubscribeTopicFromCtx(msg.Context())
	letter := msg.Copy()
	letter.Metadata.Set(ReasonDeadLetteredMetadata, detail)
	letter.Metadata.Set(TopicDeadLetteredMetadata, topic)
	letter.Metadata.Set(GroupDeadLetteredMetadata, q.group)
	if err := q.publisher.Publish(DeadLetterTopic(topic), letter); err != nil {
		return fmt.Errorf("publish %s to %s: %w", msg.UUID, DeadLetterTopic(topic), err)
	}
	q.telemetry.poisoned(msg, class)
	q.logger.Error("integration event sent to DLQ", nil, watermill.LogFields{
		"message_uuid": msg.UUID,
		"event_type":   msg.Metadata.Get(EventTypeMetadata),
		"topic":        topic,
		"error_class":  class,
		"detail":       detail,
	})
	return nil
}

// rejectUnknownEvents turns an event_type that no handler of the stream
// consumes into a permanent error, so it reaches the DLQ at once.
func rejectUnknownEvents(known map[string]map[string]bool) message.HandlerMiddleware {
	return func(next message.HandlerFunc) message.HandlerFunc {
		return func(msg *message.Message) ([]*message.Message, error) {
			topic := message.SubscribeTopicFromCtx(msg.Context())
			name := msg.Metadata.Get(EventTypeMetadata)
			if !known[topic][name] {
				return nil, Permanent(fmt.Errorf("unknown event_type %q on topic %s", name, topic))
			}
			return next(msg)
		}
	}
}
