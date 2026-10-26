// Package outbound publishes the Domain Events of the order slice as
// Integration Events (ADR 13, ADR 25): IntegrationMapper translates them to the
// Protobuf contract and the Event Envelope, Outbox hands the messages to the
// Transactional Outbox store of the active Unit of Work.
package outbound

import (
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/domains/order"
)

// OrderEventsTopic is the Kafka stream of Integration Events of orders.
const OrderEventsTopic = "orders.v1.order-events"

const aggregateType = "order"

// eventNamespace makes event_id deterministic per (Order, version, index):
// a repeated write of the same change keeps its identity.
var eventNamespace = uuid.MustParse("5b0e5f6c-6a0e-4f63-9d1e-6f1c3a9e2b10")

// contract translates one Domain Event type to its Integration Event and the
// time of the event, taken from the domain, never from the clock of the write.
type contract func(order.DomainEvent) (proto.Message, time.Time)

func contractOf[E order.DomainEvent](translate func(E) (proto.Message, time.Time)) contract {
	return func(event order.DomainEvent) (proto.Message, time.Time) { return translate(event.(E)) }
}

// contracts is the table Domain Event → Integration Event. A Domain Event
// without a row is not published and fails the write.
var contracts = map[reflect.Type]contract{
	reflect.TypeFor[order.OrderPlaced]():    contractOf(orderPlaced),
	reflect.TypeFor[order.OrderCompleted](): contractOf(orderCompleted),
	reflect.TypeFor[order.OrderCancelled](): contractOf(orderCancelled),
}

// IntegrationMapper is the infrastructure mapper Domain Event → Integration
// Event message (ADR 25). It is pure: no storage, network or clock.
type IntegrationMapper struct{}

// Map builds the message of the index-th event of the change that makes the
// Order aggregateVersion. event_id is derived from (Order, version, index), so
// a repeated write of the same change does not produce a second event.
func (IntegrationMapper) Map(aggregateID order.ID, aggregateVersion uint64, index int, event order.DomainEvent) (*message.Message, error) {
	translate, ok := contracts[reflect.TypeOf(event)]
	if !ok {
		return nil, fmt.Errorf("no integration event for domain event %T", event)
	}
	payload, occurredAt := translate(event)
	envelope := messaging.Envelope{
		EventID:          EventID(aggregateID, aggregateVersion, index),
		OccurredAt:       occurredAt,
		AggregateType:    aggregateType,
		AggregateID:      string(aggregateID),
		AggregateVersion: aggregateVersion,
	}
	msg, err := messaging.NewIntegrationEventMessage(envelope, payload)
	if err != nil {
		return nil, fmt.Errorf("integration event of order %s: %w", aggregateID, err)
	}
	msg.Metadata.Set(messaging.PartitionKeyMetadata, string(aggregateID))
	return msg, nil
}

// EventID is the deterministic event_id of the index-th event of a change.
func EventID(aggregateID order.ID, aggregateVersion uint64, index int) string {
	name := string(aggregateID) + ":" + strconv.FormatUint(aggregateVersion, 10) + ":" + strconv.Itoa(index)
	return uuid.NewSHA1(eventNamespace, []byte(name)).String()
}

func orderPlaced(e order.OrderPlaced) (proto.Message, time.Time) {
	items := make([]*ordersv1.OrderItem, 0, len(e.Items))
	for _, item := range e.Items {
		items = append(items, &ordersv1.OrderItem{
			MenuItemId: string(item.MenuItemID()),
			Quantity:   int32(item.Quantity().Value()), //nolint:gosec // quantities are small positive values validated by the Order.
			UnitPrice:  &ordersv1.Money{Amount: item.UnitPrice().Amount().String(), Currency: item.UnitPrice().Currency()},
		})
	}
	return &ordersv1.OrderPlaced{
		OrderId:      string(e.OrderID),
		CustomerId:   string(e.CustomerID),
		RestaurantId: string(e.RestaurantID),
		MarketId:     e.MarketID.String(),
		Total:        &ordersv1.Money{Amount: e.Total.Amount().String(), Currency: e.Total.Currency()},
		PlacedAt:     timestamppb.New(e.PlacedAt),
		Items:        items,
	}, e.PlacedAt
}

// orderCompleted publishes the completion moment of the domain as the event
// time: a repeated write after a failure keeps it (FR-544).
func orderCompleted(e order.OrderCompleted) (proto.Message, time.Time) {
	return &ordersv1.OrderCompleted{OrderId: string(e.OrderID), MarketId: e.MarketID.String()}, e.CompletedAt
}

func orderCancelled(e order.OrderCancelled) (proto.Message, time.Time) {
	return &ordersv1.OrderCancelled{
		OrderId:  string(e.OrderID),
		MarketId: e.MarketID.String(),
		Cancellation: &ordersv1.Cancellation{
			Outcome:     string(e.Reason.Outcome()),
			ReasonCode:  e.Reason.Code(),
			CancelledAt: timestamppb.New(e.RequestedAt),
		},
	}, e.RequestedAt
}
