package memory

import (
	"context"

	"nomnom/orders/slices/order/domains/order"
)

// OutboxRecord is the stored Outbox entry; mapping to an Integration Event and
// event_id assignment belong to the real Outbox adapter.
type OutboxRecord struct {
	AggregateType    string
	AggregateID      order.ID
	AggregateVersion uint64
	Event            order.DomainEvent
}

func (s *Store) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	defer s.lock(ctx)()
	for _, event := range events {
		s.state.outbox = append(s.state.outbox, OutboxRecord{
			AggregateType:    aggregateType,
			AggregateID:      aggregateID,
			AggregateVersion: aggregateVersion,
			Event:            event,
		})
	}
	return nil
}

// OutboxRecords returns committed and in-transaction Outbox entries.
func (s *Store) OutboxRecords() []OutboxRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]OutboxRecord(nil), s.state.outbox...)
}
