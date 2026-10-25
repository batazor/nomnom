package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"

	"nomnom/orders/pkg/messaging"
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
	tx, ok := transaction(ctx)
	if !ok {
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
		_, err = tx.Exec(ctx, `
			INSERT INTO orders_outbox (event_id, event_type, aggregate_type, aggregate_id,
				aggregate_version, occurred_at, topic, payload, traceparent)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			msg.UUID, msg.Metadata.Get(messaging.EventTypeMetadata),
			msg.Metadata.Get(messaging.AggregateTypeMetadata), msg.Metadata.Get(messaging.AggregateIDMetadata),
			version, occurredAt, topic, []byte(msg.Payload), msg.Metadata.Get(messaging.TraceParentMetadata))
		if err != nil {
			return fmt.Errorf("append outbox %s for %s: %w", msg.UUID, topic, err)
		}
	}
	// SKIP LOCKED: concurrent Units of Work do not wait on the same old rows.
	if _, err := tx.Exec(ctx, `
		DELETE FROM orders_outbox WHERE event_id IN (
			SELECT event_id FROM orders_outbox WHERE created_at < now() - $1::interval
			FOR UPDATE SKIP LOCKED)`,
		outboxRetention.String()); err != nil {
		return fmt.Errorf("outbox retention: %w", err)
	}
	return nil
}
