package outbound

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/infrastructure/messaging"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
)

// OrderRelationshipsTopic is the internal stream of RelationshipsChanged
// (ADR 18): only orders-consumer reads it and syncs the SpiceDB relationships
// of the Order. It is not an Integration Event stream.
const OrderRelationshipsTopic = "orders.v1.order-relationships"

// VersionStore stores Outbox messages and reads the stored Order version, the
// aggregate_version of a notification of a change that keeps it.
type VersionStore interface {
	MessageStore
	OrderVersion(ctx context.Context, orderID order.ID) (uint64, error)
}

// RelationshipChanges stores RelationshipsChanged in the Transactional Outbox
// of the active Unit of Work, next to the change.
type RelationshipChanges struct {
	store VersionStore
	now   func() time.Time
}

var _ syncorderrelationships.Changes = RelationshipChanges{}

func NewRelationshipChanges(store VersionStore) RelationshipChanges {
	return RelationshipChanges{store: store, now: time.Now}
}

// RelationshipsChanged stores OrderRelationshipsChanged of the Order, keyed by
// its id: notifications of one Order reach orders-consumer in order.
func (c RelationshipChanges) RelationshipsChanged(ctx context.Context, orderID order.ID) error {
	version, err := c.store.OrderVersion(ctx, orderID)
	if err != nil {
		return err
	}
	msg, err := messaging.NewIntegrationEventMessage(messaging.Envelope{
		EventID:          uuid.NewString(),
		OccurredAt:       c.now(),
		AggregateType:    aggregateType,
		AggregateID:      string(orderID),
		AggregateVersion: version,
	}, &ordersv1.OrderRelationshipsChanged{OrderId: string(orderID)})
	if err != nil {
		return fmt.Errorf("relationships change of order %s: %w", orderID, err)
	}
	msg.Metadata.Set(messaging.PartitionKeyMetadata, string(orderID))
	return c.store.AppendMessages(ctx, OrderRelationshipsTopic, msg)
}
