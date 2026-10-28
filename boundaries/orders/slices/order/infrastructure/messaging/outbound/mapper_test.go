package outbound_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	event := order.OrderCompleted{OrderID: "order-1", MarketID: ordertest.MarketID(t), PlacedAt: ordertest.PlacedAt, CompletedAt: completedAt}

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
	assert.Equal(t, ordertest.Market, payload.GetMarketId())
}

// FR-681: OrderPlaced of a Scheduled Order carries its Delivery Window; an
// ASAP Order leaves it unset.
func TestMapperPublishesDeliveryWindowOfOrderPlaced(t *testing.T) {
	start := ordertest.PlacedAt.Add(7 * time.Hour)
	window, err := order.NewDeliveryWindow(start, start.Add(15*time.Minute))
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		window order.DeliveryWindow
		want   *ordersv1.DeliveryWindow
	}{
		"scheduled": {window: window, want: &ordersv1.DeliveryWindow{Start: timestamppb.New(start), End: timestamppb.New(start.Add(15 * time.Minute))}},
		"asap":      {},
	} {
		t.Run(name, func(t *testing.T) {
			msg, err := outbound.IntegrationMapper{}.Map("order-1", 1, 0, order.OrderPlaced{
				OrderID: "order-1", CustomerID: "customer-1", RestaurantID: "restaurant-1", MarketID: ordertest.MarketID(t),
				Total: ordertest.Money(t, "20"), PlacedAt: ordertest.PlacedAt, DeliveryWindow: tc.window,
			})
			require.NoError(t, err)

			var payload ordersv1.OrderPlaced
			require.NoError(t, proto.Unmarshal(msg.Payload, &payload))
			assert.True(t, proto.Equal(tc.want, payload.GetDeliveryWindow()), "%v", payload.GetDeliveryWindow())
		})
	}
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
			MarketID: placed.MarketID(), Total: placed.Total(), PlacedAt: placed.PlacedAt(), Items: placed.Items()},
		order.OrderCancelled{OrderID: "order-1", MarketID: placed.MarketID(), RequestedAt: ordertest.PlacedAt.Add(time.Minute), Reason: cancelled},
	}))
	require.NoError(t, outbound.NewOutbox(store).Append(context.Background(), "order-1", 3, nil))

	assert.Equal(t, outbound.OrderEventsTopic, store.topic)
	require.Len(t, store.messages, 2)
	assert.Equal(t, outbound.EventID("order-1", 2, 0), store.messages[0].UUID)
	assert.Equal(t, outbound.EventID("order-1", 2, 1), store.messages[1].UUID)
	assert.Equal(t, "nomnom.orders.v1.OrderCancelled", store.messages[1].Metadata.Get(messaging.EventTypeMetadata))

	// ADR 41: every Integration Event carries the Market of the Order.
	var placedPayload ordersv1.OrderPlaced
	require.NoError(t, proto.Unmarshal(store.messages[0].Payload, &placedPayload))
	assert.Equal(t, ordertest.Market, placedPayload.GetMarketId())
	var cancelledPayload ordersv1.OrderCancelled
	require.NoError(t, proto.Unmarshal(store.messages[1].Payload, &cancelledPayload))
	assert.Equal(t, ordertest.Market, cancelledPayload.GetMarketId())
}
