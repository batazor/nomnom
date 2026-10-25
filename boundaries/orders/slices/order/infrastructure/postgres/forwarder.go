package postgres

import (
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	watermillsql "github.com/ThreeDotsLabs/watermill-sql/v4/pkg/sql"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"
	"github.com/ThreeDotsLabs/watermill/message"
)

// ForwarderHandlerName distinguishes the Forwarder from consumer handlers in
// handler_name of Watermill router metrics (ADR 28).
const ForwarderHandlerName = "orders-outbox-forwarder"

// NewForwarder moves Outbox records to the Kafka publisher, each to the topic
// it was appended for. It must run as a single active instance (ADR 13).
// middlewares decorate the forwarding handler (Watermill router metrics of the
// forwarder role).
func NewForwarder(s *Store, out message.Publisher, logger watermill.LoggerAdapter, middlewares ...message.HandlerMiddleware) (*forwarder.Forwarder, error) {
	subscriber, err := watermillsql.NewSubscriber(watermillsql.BeginnerFromPgx(s.pool), watermillsql.SubscriberConfig{
		ConsumerGroup:  "orders-forwarder",
		SchemaAdapter:  watermillsql.DefaultPostgreSQLSchema{},
		OffsetsAdapter: watermillsql.DefaultPostgreSQLOffsetsAdapter{},
		PollInterval:   200 * time.Millisecond,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("outbox subscriber: %w", err)
	}
	fwd, err := forwarder.NewForwarder(subscriber, out, logger, forwarder.Config{ForwarderTopic: OutboxTopic, HandlerName: ForwarderHandlerName, Middlewares: middlewares})
	if err != nil {
		return nil, fmt.Errorf("outbox forwarder: %w", err)
	}
	return fwd, nil
}
