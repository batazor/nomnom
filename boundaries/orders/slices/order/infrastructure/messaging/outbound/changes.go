package outbound

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/domains/order"
)

// OrderChangesTopic is the Kafka stream of Order Change Notifications (ADR 36):
// the BFF Router turns them into GraphQL subscription events. It is not an
// Integration Event stream: no bounded context consumes it.
const OrderChangesTopic = "orders.v1.order-changes"

// changeNotification is the Cosmo Streams event of an Order entity: only the
// key, the Router resolves the current Order through the BFF with the
// subscriber's access token, so the notification carries no order data.
type changeNotification struct {
	Typename string `json:"__typename"`
	ID       string `json:"id"`
}

// ChangeNotifier stores an Order Change Notification in the Transactional
// Outbox of the active Unit of Work, next to the change itself.
type ChangeNotifier struct {
	store MessageStore
}

func NewChangeNotifier(store MessageStore) ChangeNotifier {
	return ChangeNotifier{store: store}
}

// changeNotificationType is the event_type header of Order Change Notifications.
const changeNotificationType = "OrderChanged"

// Notify stores the notification of the Order written as aggregateVersion,
// with the Event Envelope every Outbox row carries (ADR 37).
func (n ChangeNotifier) Notify(ctx context.Context, id order.ID, aggregateVersion uint64, at time.Time) error {
	payload, err := json.Marshal(changeNotification{Typename: "Order", ID: string(id)})
	if err != nil {
		return fmt.Errorf("change notification of order %s: %w", id, err)
	}
	eventID := uuid.NewString()
	msg := message.NewMessage(eventID, payload)
	msg.Metadata.Set(messaging.EventIDMetadata, eventID)
	msg.Metadata.Set(messaging.EventTypeMetadata, changeNotificationType)
	msg.Metadata.Set(messaging.OccurredAtMetadata, at.UTC().Format(time.RFC3339Nano))
	msg.Metadata.Set(messaging.AggregateTypeMetadata, aggregateType)
	msg.Metadata.Set(messaging.AggregateIDMetadata, string(id))
	msg.Metadata.Set(messaging.AggregateVersionMetadata, strconv.FormatUint(aggregateVersion, 10))
	msg.Metadata.Set(messaging.PartitionKeyMetadata, string(id))
	return n.store.AppendMessages(ctx, OrderChangesTopic, msg)
}
