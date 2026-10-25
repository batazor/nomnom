package messaging

import (
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
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

// RetryPolicy bounds the redelivery of transient failures.
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
	PoisonTopic  string
	Retry        RetryPolicy
	CloseTimeout time.Duration
}

// NewConsumerRouter builds the Watermill router of the consumer process: one
// EventGroupProcessor handler group per stream and the error policy of ADR 28:
//
//	transient         → retry with backoff; exhausted → poison topic + ack
//	permanent/unknown → poison topic + ack
//	business          → ack, logged
//	nil (incl. Inbox duplicate) → ack
func NewConsumerRouter(
	cfg ConsumerConfig,
	subscriber message.Subscriber,
	poison message.Publisher,
	streams []Stream,
	logger watermill.LoggerAdapter,
) (*message.Router, error) {
	if cfg.PoisonTopic == "" {
		return nil, fmt.Errorf("poison topic must not be empty")
	}
	if logger == nil {
		logger = watermill.NopLogger{}
	}

	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: cfg.CloseTimeout}, logger)
	if err != nil {
		return nil, fmt.Errorf("create router: %w", err)
	}

	poisonQueue, err := middleware.PoisonQueue(poison, cfg.PoisonTopic)
	if err != nil {
		return nil, fmt.Errorf("create poison queue middleware: %w", err)
	}
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
	router.AddMiddleware(poisonQueue, retry.Middleware, rejectUnknownEvents(known))

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

// rejectUnknownEvents turns an event_type that no handler of the stream
// consumes into a permanent error, so it reaches the poison topic at once.
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
