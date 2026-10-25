package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ThreeDotsLabs/watermill"
	watermillsql "github.com/ThreeDotsLabs/watermill-sql/v4/pkg/sql"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"
	"github.com/ThreeDotsLabs/watermill/message"

	"nomnom/orders/pkg/messaging"
)

// OutboxTopic is the watermill-sql topic read by the Forwarder (ADR 13).
const OutboxTopic = "orders_outbox"

// ErrOutboxOutsideTransaction: Outbox records exist only with a business change.
var ErrOutboxOutsideTransaction = errors.New("outbox append requires an active unit of work")

// AppendMessages stores ready Integration Event messages for topic in the
// active transaction; the Forwarder publishes them after commit. Mapping of
// Domain Events belongs to messaging/outbound.
func (s *Store) AppendMessages(ctx context.Context, topic string, messages ...*message.Message) error {
	if len(messages) == 0 {
		return nil
	}
	tx, ok := transaction(ctx)
	if !ok {
		return ErrOutboxOutsideTransaction
	}
	publisher, err := watermillsql.NewPublisher(watermillsql.TxFromPgx(tx), watermillsql.PublisherConfig{
		SchemaAdapter: watermillsql.DefaultPostgreSQLSchema{},
	}, watermill.NopLogger{})
	if err != nil {
		return fmt.Errorf("outbox publisher: %w", err)
	}
	// Trace context операции, записавшей событие, едет в metadata до Kafka headers.
	messaging.InjectTraceContext(ctx, messages...)
	outbox := forwarder.NewPublisher(publisher, forwarder.PublisherConfig{ForwarderTopic: OutboxTopic})
	if err := outbox.Publish(topic, messages...); err != nil {
		return fmt.Errorf("append outbox for %s: %w", topic, err)
	}
	return nil
}
