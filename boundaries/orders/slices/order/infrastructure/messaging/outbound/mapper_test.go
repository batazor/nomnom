package outbound_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
)

// AC-634: OrderCompleted is published at the completion moment of the domain,
// whenever the message is written.
func TestMapperPublishesOrderCompletedAtCompletionMoment(t *testing.T) {
	completedAt := ordertest.PlacedAt.Add(90 * time.Minute)
	event := order.OrderCompleted{OrderID: "order-1", PlacedAt: ordertest.PlacedAt, CompletedAt: completedAt}

	first, err := outbound.IntegrationMapper{}.Map("order-1", 3, 0, event)
	require.NoError(t, err)
	repeated, err := outbound.IntegrationMapper{}.Map("order-1", 3, 0, event)
	require.NoError(t, err)

	envelope, err := messaging.EnvelopeFromMessage(first)
	require.NoError(t, err)
	assert.True(t, completedAt.Equal(envelope.OccurredAt))
	assert.Equal(t, "nomnom.orders.v1.OrderCompleted", envelope.EventType)
	assert.Equal(t, uint64(3), envelope.AggregateVersion)
	assert.Equal(t, outbound.EventID("order-1", 3, 0), envelope.EventID)
	assert.Equal(t, first.UUID, repeated.UUID, "a repeated write keeps event_id")
	assert.Equal(t, first.Metadata, repeated.Metadata)
	assert.Equal(t, "order-1", first.Metadata.Get(messaging.PartitionKeyMetadata))

	var payload ordersv1.OrderCompleted
	require.NoError(t, proto.Unmarshal(first.Payload, &payload))
	assert.Equal(t, "order-1", payload.GetOrderId())
}

func TestMapperEventIDDependsOnChangeAndIndex(t *testing.T) {
	ids := map[string]struct{}{
		outbound.EventID("order-1", 1, 0): {},
		outbound.EventID("order-1", 1, 1): {},
		outbound.EventID("order-1", 2, 0): {},
		outbound.EventID("order-2", 1, 0): {},
	}
	assert.Len(t, ids, 4)
}

type unknownEvent struct{ order.DomainEvent }

func TestMapperRejectsEventWithoutContract(t *testing.T) {
	_, err := outbound.IntegrationMapper{}.Map("order-1", 1, 0, unknownEvent{})
	require.ErrorContains(t, err, "no integration event")
}

type recordingStore struct {
	topic    string
	messages []*message.Message
}

func (s *recordingStore) AppendMessages(_ context.Context, topic string, messages ...*message.Message) error {
	s.topic, s.messages = topic, append(s.messages, messages...)
	return nil
}

func TestOutboxMapsEventsAndOnlyHandsThemToTheStore(t *testing.T) {
	store := &recordingStore{}
	placed := ordertest.New(t, "order-1")
	cancelled, err := order.NewCancellationReason(order.OutcomeFailure, "payment_declined")
	require.NoError(t, err)

	require.NoError(t, outbound.NewOutbox(store).Append(context.Background(), "order-1", 2, []order.DomainEvent{
		order.OrderPlaced{OrderID: "order-1", CustomerID: placed.CustomerID(), RestaurantID: placed.RestaurantID(),
			Total: placed.Total(), PlacedAt: placed.PlacedAt(), Items: placed.Items()},
		order.OrderCancelled{OrderID: "order-1", RequestedAt: ordertest.PlacedAt.Add(time.Minute), Reason: cancelled},
	}))
	require.NoError(t, outbound.NewOutbox(store).Append(context.Background(), "order-1", 3, nil))

	assert.Equal(t, outbound.OrderEventsTopic, store.topic)
	require.Len(t, store.messages, 2)
	assert.Equal(t, outbound.EventID("order-1", 2, 0), store.messages[0].UUID)
	assert.Equal(t, outbound.EventID("order-1", 2, 1), store.messages[1].UUID)
	assert.Equal(t, "nomnom.orders.v1.OrderCancelled", store.messages[1].Metadata.Get(messaging.EventTypeMetadata))
}
