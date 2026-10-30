package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/ThreeDotsLabs/watermill/message"

	"nomnom/orders/infrastructure/messaging"
)

// ErrOutboxOutsideTransaction: Outbox records exist only with a business change.
var ErrOutboxOutsideTransaction = errors.New("outbox append requires an active unit of work")

// outboxRetention keeps rows only as long as they may be needed: Debezium
// reads them from the WAL right after commit, the table is not the queue.
const outboxRetention = time.Hour

// AppendMessages stores ready messages for topic in the active transaction;
// Debezium publishes each row after commit (ADR 37). The Event Envelope comes
// from the message metadata. Mapping of Domain Events belongs to
// messaging/outbound.
func (s *Store) AppendMessages(ctx context.Context, topic string, messages ...*message.Message) error {
	if len(messages) == 0 {
		return nil
	}
	if _, ok := transaction(ctx); !ok {
		return ErrOutboxOutsideTransaction
	}
	// Trace context операции, записавшей событие, едет в Outbox до Kafka headers.
	messaging.InjectTraceContext(ctx, messages...)
	for _, msg := range messages {
		version, err := strconv.ParseUint(msg.Metadata.Get(messaging.AggregateVersionMetadata), 10, 64)
		if err != nil {
			return fmt.Errorf("outbox %s aggregate_version: %w", msg.UUID, err)
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, msg.Metadata.Get(messaging.OccurredAtMetadata))
		if err != nil {
			return fmt.Errorf("outbox %s occurred_at: %w", msg.UUID, err)
		}
		_, err = s.exec(ctx, psql.Insert("orders_outbox").
			Columns("event_id", "event_type", "aggregate_type", "aggregate_id",
				"aggregate_version", "occurred_at", "topic", "payload", "traceparent").
			Values(msg.UUID, msg.Metadata.Get(messaging.EventTypeMetadata),
				msg.Metadata.Get(messaging.AggregateTypeMetadata), msg.Metadata.Get(messaging.AggregateIDMetadata),
				version, occurredAt, topic, []byte(msg.Payload), msg.Metadata.Get(messaging.TraceParentMetadata)))
		if err != nil {
			return fmt.Errorf("append outbox %s for %s: %w", msg.UUID, topic, err)
		}
	}
	// SKIP LOCKED: concurrent Units of Work do not wait on the same old rows.
	expired := psql.Select("event_id").From("orders_outbox").
		Where("created_at < now() - ?::interval", outboxRetention.String()).
		Suffix("FOR UPDATE SKIP LOCKED")
	if _, err := s.exec(ctx, psql.Delete("orders_outbox").Where(sq.Expr("event_id IN (?)", expired))); err != nil {
		return fmt.Errorf("outbox retention: %w", err)
	}
	return nil
}
