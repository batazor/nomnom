package outbound

import (
	"context"

	"github.com/ThreeDotsLabs/watermill/message"

	"nomnom/orders/slices/order/domains/order"
)

// MessageStore persists Integration Event messages for a destination topic in
// the Transactional Outbox of the active Unit of Work; Debezium publishes them
// from the WAL after commit (ADR 37).
type MessageStore interface {
	AppendMessages(ctx context.Context, topic string, messages ...*message.Message) error
}

// Outbox is the Outbox port of the use cases: it maps Domain Events with
// IntegrationMapper and stores the messages; it neither publishes nor commits.
type Outbox struct {
	mapper IntegrationMapper
	store  MessageStore
}

func NewOutbox(store MessageStore) Outbox {
	return Outbox{store: store}
}

func (o Outbox) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	if len(events) == 0 {
		return nil
	}
	messages := make([]*message.Message, 0, len(events))
	for index, event := range events {
		msg, err := o.mapper.Map(aggregateID, aggregateVersion, index, event)
		if err != nil {
			return err
		}
		messages = append(messages, msg)
	}
	return o.store.AppendMessages(ctx, OrderEventsTopic, messages...)
}
