package messaging_test

import (
	"context"
	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recordcourierrelease"
	"nomnom/orders/slices/order/applications/recorddeliverycompletion"
	"nomnom/orders/slices/order/applications/recorddeliveryfailure"
	"nomnom/orders/slices/order/applications/recordorderready"
	"nomnom/orders/slices/order/applications/recordpickup"
	"nomnom/orders/slices/order/applications/recordpreparationdelay"
	"nomnom/orders/slices/order/applications/recordpreparationstarted"
	"nomnom/orders/slices/order/applications/rejectorder"
	"nomnom/orders/slices/order/applications/relaypreparationissue"
	"nomnom/orders/slices/order/applications/startfulfillment"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	ordermessaging "nomnom/orders/slices/order/infrastructure/messaging"
	deliveryv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/delivery/v1"
	restaurantsv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

type consumer struct {
	pubSub        *gochannel.GoChannel
	store         *repositorytest.Store
	starter       *MockStarter
	notifier      *MockNotifier
	relationships *MockRelationships
	poison        chan *message.Message
}

// startConsumer wires the order slice streams to gochannel: the same router
// policy as orders-consumer, without Kafka. The workflow and relationship
// ports expect no call until a test sets its expectations.
func startConsumer(t *testing.T, orders ...*order.Order) consumer {
	t.Helper()
	store := repositorytest.Open(t)
	for _, o := range orders {
		require.NoError(t, store.Add(context.Background(), o))
	}
	starter, notifier, relationships := NewMockStarter(t), NewMockNotifier(t), NewMockRelationships(t)
	handlers := ordermessaging.Handlers{
		ConfirmOrder:             confirmorder.NewHandler(store, store, store, notifier),
		RejectOrder:              rejectorder.NewHandler(store, store, store, notifier),
		RecordPreparationStarted: recordpreparationstarted.NewHandler(store, store, store, notifier),
		RecordOrderReady:         recordorderready.NewHandler(store, store, store, notifier),
		RecordPreparationDelay:   recordpreparationdelay.NewHandler(store, store, store, notifier),
		RelayPreparationIssue:    relaypreparationissue.NewHandler(notifier),
		RecordCourierAssignment:  recordcourierassignment.NewHandler(store, store, store, notifier, store, store),
		RecordCourierRelease:     recordcourierrelease.NewHandler(store, store, store, store),
		RecordPickup:             recordpickup.NewHandler(store, store, store, notifier),
		RecordDeliveryCompletion: recorddeliverycompletion.NewHandler(store, store, store, notifier),
		RecordDeliveryFailure:    recorddeliveryfailure.NewHandler(store, store, store, notifier),
		SyncOrderRelationships:   syncorderrelationships.NewHandler(store, store, relationships, store),
		StartFulfillment:         startfulfillment.NewHandler(store, store, store, starter),
	}

	pubSub := gochannel.NewGoChannel(gochannel.Config{BlockPublishUntilSubscriberAck: true}, watermill.NopLogger{})
	poison := make(chan *message.Message, 10)
	for _, stream := range []string{ordermessaging.RestaurantOrderEventsTopic, ordermessaging.DeliveryEventsTopic, outbound.OrderEventsTopic} {
		subscription, err := pubSub.Subscribe(context.Background(), messaging.DeadLetterTopic(stream))
		require.NoError(t, err)
		go func() {
			for msg := range subscription {
				msg.Ack()
				poison <- msg
			}
		}()
	}

	router, err := messaging.NewConsumerRouter(messaging.ConsumerConfig{
		ConsumerGroup: "orders",
		Retry:         messaging.RetryPolicy{MaxRetries: 1, InitialInterval: time.Millisecond, MaxInterval: time.Millisecond},
	}, pubSub, pubSub, ordermessaging.Streams(handlers), nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = router.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = router.Close()
	})
	<-router.Running()
	return consumer{pubSub: pubSub, store: store, starter: starter, notifier: notifier, relationships: relationships, poison: poison}
}

func (c consumer) publish(t *testing.T, topic, eventID string, event proto.Message) {
	t.Helper()
	c.publishVersion(t, topic, eventID, 1, event)
}

// publishVersion publishes event with aggregate_version version.
func (c consumer) publishVersion(t *testing.T, topic, eventID string, version uint64, event proto.Message) {
	t.Helper()
	msg, err := messaging.NewIntegrationEventMessage(messaging.Envelope{
		EventID:          eventID,
		OccurredAt:       occurredAt,
		AggregateType:    "source",
		AggregateID:      "aggregate-1",
		AggregateVersion: version,
	}, event)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- c.pubSub.Publish(topic, msg) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was not acked", eventID)
	}
}

func (c consumer) poisoned(t *testing.T) *message.Message {
	t.Helper()
	select {
	case msg := <-c.poison:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message in DLQ")
		return nil
	}
}

func (c consumer) noPoison(t *testing.T) {
	t.Helper()
	select {
	case msg := <-c.poison:
		t.Fatalf("unexpected poison message %s: %s", msg.UUID, msg.Metadata.Get(messaging.ReasonDeadLetteredMetadata))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRestaurantOrderAcceptedConfirmsOrder(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))
	c.notifier.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
	}).Return(nil).Once()

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", &restaurantsv1.RestaurantOrderAccepted{OrderId: "order-1"})

	snapshot, _ := c.store.Snapshot("order-1")
	assert.Equal(t, order.StateConfirmed, snapshot.State)
	assert.Equal(t, 1, c.store.InboxSize())
	c.noPoison(t)
}

// FR-638: the own OrderPlaced starts the workflow of the Order.
func TestOrderPlacedStartsWorkflow(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))
	c.starter.EXPECT().Start(mock.Anything, fulfillment.StartRequest{OrderID: "order-1", EventID: "event-placed", OccurredAt: occurredAt}).
		Return(nil).Once()

	c.publish(t, outbound.OrderEventsTopic, "event-placed", &ordersv1.OrderPlaced{OrderId: "order-1"})

	assert.Equal(t, 1, c.store.InboxSize())
	c.noPoison(t)
}

// FR-415: OrderPlaced of a Scheduled Order starts the workflow with its window.
func TestScheduledOrderPlacedStartsWorkflowWithWindow(t *testing.T) {
	start := ordertest.PlacedAt.Add(7 * time.Hour)
	window, err := order.NewDeliveryWindow(start, start.Add(15*time.Minute))
	require.NoError(t, err)
	c := startConsumer(t, ordertest.New(t, "order-1", ordertest.WithDeliveryWindow(t, start, start.Add(15*time.Minute))))
	c.starter.EXPECT().Start(mock.Anything, fulfillment.StartRequest{
		OrderID: "order-1", EventID: "event-placed", OccurredAt: occurredAt, DeliveryWindow: window,
	}).Return(nil).Once()

	c.publish(t, outbound.OrderEventsTopic, "event-placed", &ordersv1.OrderPlaced{
		OrderId:        "order-1",
		DeliveryWindow: &ordersv1.DeliveryWindow{Start: timestamppb.New(start), End: timestamppb.New(start.Add(15 * time.Minute))},
	})

	c.noPoison(t)
}

// AC-822: own OrderCompleted and OrderCancelled are acknowledged without a dead letter.
func TestOwnFinalEventsAreAcknowledged(t *testing.T) {
	// The workflow is not started: c.starter expects no call.
	c := startConsumer(t)

	c.publish(t, outbound.OrderEventsTopic, "event-completed", &ordersv1.OrderCompleted{OrderId: "order-1"})
	c.publish(t, outbound.OrderEventsTopic, "event-cancelled", &ordersv1.OrderCancelled{OrderId: "order-1"})

	assert.Zero(t, c.store.InboxSize())
	c.noPoison(t)
}

func TestRedeliveryIsAckedAsDuplicate(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))
	accepted := &restaurantsv1.RestaurantOrderAccepted{OrderId: "order-1"}
	// The duplicate re-notifies the workflow with the same event_id.
	c.notifier.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
	}).Return(nil).Times(2)

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", accepted)
	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", accepted)

	assert.Equal(t, 1, c.store.InboxSize())
	c.noPoison(t)
}

func TestDeliveryStreamKeepsOrderOfOneAggregate(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed)))
	assigned := c.notifier.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: occurredAt,
	}).Return(nil).Once()
	pickedUp := c.notifier.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-2", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
	}).Return(nil).Once()
	pickedUp.NotBefore(assigned)
	c.notifier.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-3", Kind: fulfillment.KindDeliveryCompleted, OccurredAt: occurredAt,
	}).Return(nil).Once().NotBefore(pickedUp)

	c.publish(t, ordermessaging.DeliveryEventsTopic, "event-1", &deliveryv1.CourierAssignedToDelivery{OrderId: "order-1"})
	c.publish(t, ordermessaging.DeliveryEventsTopic, "event-2", &deliveryv1.DeliveryPickedUp{OrderId: "order-1"})
	c.publish(t, ordermessaging.DeliveryEventsTopic, "event-3", &deliveryv1.DeliveryCompleted{OrderId: "order-1"})

	assert.Equal(t, 3, c.store.InboxSize())
	c.noPoison(t)
}

// linkedDelivery is the stored Delivery: the subject of order#delivery that
// Sync Order Relationships writes (ADR 18).
func linkedDelivery(c consumer) string {
	stored, _, _ := c.store.CourierAssignment(context.Background(), "order-1")
	return stored.DeliveryID
}

// AC-518, AC-534, AC-535: the assignment links order#delivery,
// CourierUnassignedFromDelivery keeps it and DeliveryCancelled deletes it,
// without notifying the workflow.
func TestCourierEventsKeepDeliveryLink(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed)))
	assigned := &deliveryv1.CourierAssignedToDelivery{OrderId: "order-1", DeliveryId: "delivery-1", CourierId: "courier-1"}
	// Only the assignment notifies the workflow.
	c.notifier.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: occurredAt,
	}).Return(nil).Once()

	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "event-1", 1, assigned)
	require.Equal(t, "delivery-1", linkedDelivery(c))
	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "event-2", 2,
		&deliveryv1.CourierUnassignedFromDelivery{OrderId: "order-1", DeliveryId: "delivery-1", CourierId: "courier-1"})
	assert.Equal(t, "delivery-1", linkedDelivery(c), "delivery revokes the courier")
	assert.Len(t, c.store.RelationshipChanges(), 1, "the unassignment keeps order#delivery")
	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "event-3", 3,
		&deliveryv1.DeliveryCancelled{OrderId: "order-1", DeliveryId: "delivery-1", Reason: "customer request"})
	assert.Empty(t, linkedDelivery(c))

	stored, found, err := c.store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{AssignedAt: occurredAt, DeliveryVersion: 3}, stored, "aggregate_version orders courier events")
	assert.Equal(t, 3, c.store.InboxSize())
	c.noPoison(t)
}

func TestPreparationDelayedIsRelayedToWorkflow(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))
	expected := occurredAt.Add(15 * time.Minute)
	c.notifier.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationDelayed, OccurredAt: occurredAt, ExpectedReadyAt: expected,
	}).Return(nil).Once()

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", &restaurantsv1.PreparationDelayed{
		OrderId: "order-1", ExpectedReadyAt: timestamppb.New(expected),
	})

	assert.Equal(t, 1, c.store.InboxSize())
	c.noPoison(t)
}

// FR-611, AC-771: the consumer carries the deadlines of the events into the
// Order; a delay replaces the ready time, a reassignment the pickup time.
func TestEventsCarryExpectedTimesIntoOrder(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))
	readyAt, delayedAt := occurredAt.Add(12*time.Minute), occurredAt.Add(20*time.Minute)
	firstPickup, secondPickup := occurredAt.Add(25*time.Minute), occurredAt.Add(30*time.Minute)
	deliveredAt := occurredAt.Add(45 * time.Minute)
	for eventID, notification := range map[string]fulfillment.Notification{
		"accepted":   {Kind: fulfillment.KindRestaurantAccepted},
		"delayed":    {Kind: fulfillment.KindPreparationDelayed, ExpectedReadyAt: delayedAt},
		"assigned-1": {Kind: fulfillment.KindCourierAssigned},
		"assigned-2": {Kind: fulfillment.KindCourierAssigned},
		"picked-up":  {Kind: fulfillment.KindPickedUp},
	} {
		notification.OrderID, notification.EventID, notification.OccurredAt = "order-1", eventID, occurredAt
		c.notifier.EXPECT().Notify(mock.Anything, notification).Return(nil).Once()
	}

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "accepted", &restaurantsv1.RestaurantOrderAccepted{
		OrderId: "order-1", ExpectedReadyAt: timestamppb.New(readyAt),
	})
	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "delayed", &restaurantsv1.PreparationDelayed{
		OrderId: "order-1", ExpectedReadyAt: timestamppb.New(delayedAt),
	})
	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "assigned-1", 2, &deliveryv1.CourierAssignedToDelivery{
		OrderId: "order-1", DeliveryId: "delivery-1", CourierId: "courier-1", ExpectedPickupAt: timestamppb.New(firstPickup),
	})
	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "assigned-2", 4, &deliveryv1.CourierAssignedToDelivery{
		OrderId: "order-1", DeliveryId: "delivery-1", CourierId: "courier-2", ExpectedPickupAt: timestamppb.New(secondPickup),
	})
	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "picked-up", 5, &deliveryv1.DeliveryPickedUp{
		OrderId: "order-1", ExpectedDeliveredAt: timestamppb.New(deliveredAt),
	})

	snapshot, _ := c.store.Snapshot("order-1")
	assert.Equal(t, order.ExpectedTimes{ReadyAt: delayedAt, PickupAt: secondPickup, DeliveredAt: deliveredAt}, snapshot.Expected)
	c.noPoison(t)
}

func TestUnknownOrderGoesToDLQ(t *testing.T) {
	c := startConsumer(t)

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", &restaurantsv1.RestaurantOrderReady{OrderId: "missing"})

	assert.Equal(t, "event-1", c.poisoned(t).UUID)
	assert.Zero(t, c.store.InboxSize(), "rolled back transaction keeps no Inbox record")
}

func TestEventOfAnotherStreamGoesToDLQ(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", &deliveryv1.DeliveryPickedUp{OrderId: "order-1"})

	// The workflow is not notified: c.notifier expects no call.
	assert.Equal(t, ordermessaging.RestaurantOrderEventsTopic, c.poisoned(t).Metadata.Get(messaging.TopicDeadLetteredMetadata))
}

func TestInvalidCommandGoesToDLQ(t *testing.T) {
	c := startConsumer(t)

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", &restaurantsv1.PreparationDelayed{OrderId: "order-1"})

	assert.Equal(t, "event-1", c.poisoned(t).UUID)
}

// ADR 18: RelationshipsChanged on the internal stream syncs the relationships
// of the Order from its stored state, without the Inbox.
func TestRelationshipsChangedSyncsTheOrderState(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))
	c.relationships.EXPECT().SyncOrderRelationships(mock.Anything, order.ID("order-1"),
		syncorderrelationships.Subjects{CustomerID: "customer-1", RestaurantID: "restaurant-1"}).
		Return(application.AuthorizationRevision("zedtoken-1"), nil).Once()

	c.publish(t, outbound.OrderRelationshipsTopic, "event-1", &ordersv1.OrderRelationshipsChanged{OrderId: "order-1"})

	revision, found, err := c.store.Revision(context.Background(), "order-1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, application.AuthorizationRevision("zedtoken-1"), revision)
	assert.Equal(t, 0, c.store.InboxSize())
	c.noPoison(t)
}
