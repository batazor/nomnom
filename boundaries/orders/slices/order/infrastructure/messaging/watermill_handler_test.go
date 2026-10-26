package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/stretchr/testify/assert"
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
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	ordermessaging "nomnom/orders/slices/order/infrastructure/messaging"
	deliveryv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/delivery/v1"
	restaurantsv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

type consumer struct {
	pubSub     *gochannel.GoChannel
	store      *repositorytest.Store
	workflow   *fake.Workflow
	authorizer *fake.Authorizer
	poison     chan *message.Message
}

// startConsumer wires the order slice streams to gochannel: the same router
// policy as orders-consumer, without Kafka.
func startConsumer(t *testing.T, orders ...*order.Order) consumer {
	t.Helper()
	store := repositorytest.Open(t)
	for _, o := range orders {
		require.NoError(t, store.Add(context.Background(), o))
	}
	workflow := fake.NewWorkflow()
	authorizer := fake.NewAuthorizer()
	handlers := ordermessaging.Handlers{
		ConfirmOrder:             confirmorder.NewHandler(store, store, store, workflow),
		RejectOrder:              rejectorder.NewHandler(store, store, store, workflow),
		RecordPreparationStarted: recordpreparationstarted.NewHandler(store, store, store, workflow),
		RecordOrderReady:         recordorderready.NewHandler(store, store, store, workflow),
		RecordPreparationDelay:   recordpreparationdelay.NewHandler(store, store, store, workflow),
		RelayPreparationIssue:    relaypreparationissue.NewHandler(workflow),
		RecordCourierAssignment:  recordcourierassignment.NewHandler(store, store, store, workflow, store, authorizer),
		RecordCourierRelease:     recordcourierrelease.NewHandler(store, store, store, authorizer),
		RecordPickup:             recordpickup.NewHandler(store, store, store, workflow),
		RecordDeliveryCompletion: recorddeliverycompletion.NewHandler(store, store, store, workflow),
		RecordDeliveryFailure:    recorddeliveryfailure.NewHandler(store, store, store, workflow),
	}

	pubSub := gochannel.NewGoChannel(gochannel.Config{BlockPublishUntilSubscriberAck: true}, watermill.NopLogger{})
	poison := make(chan *message.Message, 10)
	for _, stream := range []string{ordermessaging.RestaurantOrderEventsTopic, ordermessaging.DeliveryEventsTopic} {
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
	return consumer{pubSub: pubSub, store: store, workflow: workflow, authorizer: authorizer, poison: poison}
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

func kinds(notifications []fulfillment.Notification) []fulfillment.Kind {
	result := make([]fulfillment.Kind, 0, len(notifications))
	for _, n := range notifications {
		result = append(result, n.Kind)
	}
	return result
}

func TestRestaurantOrderAcceptedConfirmsOrder(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", &restaurantsv1.RestaurantOrderAccepted{OrderId: "order-1"})

	snapshot, _ := c.store.Snapshot("order-1")
	assert.Equal(t, order.StateConfirmed, snapshot.State)
	assert.Equal(t, 1, c.store.InboxSize())
	assert.Equal(t, []fulfillment.Notification{{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
	}}, c.workflow.Notifications())
	c.noPoison(t)
}

func TestRedeliveryIsAckedAsDuplicate(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))
	accepted := &restaurantsv1.RestaurantOrderAccepted{OrderId: "order-1"}

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", accepted)
	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", accepted)

	assert.Equal(t, 1, c.store.InboxSize())
	assert.Len(t, c.workflow.Notifications(), 2, "duplicate re-notifies the workflow with the same event_id")
	c.noPoison(t)
}

func TestDeliveryStreamKeepsOrderOfOneAggregate(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed)))

	c.publish(t, ordermessaging.DeliveryEventsTopic, "event-1", &deliveryv1.CourierAssignedToDelivery{OrderId: "order-1"})
	c.publish(t, ordermessaging.DeliveryEventsTopic, "event-2", &deliveryv1.DeliveryPickedUp{OrderId: "order-1"})
	c.publish(t, ordermessaging.DeliveryEventsTopic, "event-3", &deliveryv1.DeliveryCompleted{OrderId: "order-1"})

	assert.Equal(t, []fulfillment.Kind{
		fulfillment.KindCourierAssigned, fulfillment.KindPickedUp, fulfillment.KindDeliveryCompleted,
	}, kinds(c.workflow.Notifications()))
	assert.Equal(t, 3, c.store.InboxSize())
	c.noPoison(t)
}

func linkedDelivery(c consumer) string {
	deliveryID, _ := c.authorizer.DeliveryOf("order-1")
	return deliveryID
}

// AC-518, AC-534, AC-535: the assignment links order#delivery,
// CourierUnassignedFromDelivery keeps it and DeliveryCancelled deletes it,
// without notifying the workflow.
func TestCourierEventsKeepDeliveryLink(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed)))
	assigned := &deliveryv1.CourierAssignedToDelivery{OrderId: "order-1", DeliveryId: "delivery-1", CourierId: "courier-1"}

	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "event-1", 1, assigned)
	require.Equal(t, "delivery-1", linkedDelivery(c))
	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "event-2", 2,
		&deliveryv1.CourierUnassignedFromDelivery{OrderId: "order-1", DeliveryId: "delivery-1", CourierId: "courier-1"})
	assert.Equal(t, "delivery-1", linkedDelivery(c), "delivery revokes the courier")
	assert.Equal(t, 1, c.authorizer.DeliveryWrites())
	c.publishVersion(t, ordermessaging.DeliveryEventsTopic, "event-3", 3,
		&deliveryv1.DeliveryCancelled{OrderId: "order-1", DeliveryId: "delivery-1", Reason: "customer request"})
	assert.Empty(t, linkedDelivery(c))

	stored, found, err := c.store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{AssignedAt: occurredAt, DeliveryVersion: 3}, stored, "aggregate_version orders courier events")
	assert.Equal(t, []fulfillment.Kind{fulfillment.KindCourierAssigned}, kinds(c.workflow.Notifications()))
	assert.Equal(t, 3, c.store.InboxSize())
	c.noPoison(t)
}

func TestPreparationDelayedIsRelayedToWorkflow(t *testing.T) {
	c := startConsumer(t, ordertest.New(t, "order-1"))
	expected := occurredAt.Add(15 * time.Minute)

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", &restaurantsv1.PreparationDelayed{
		OrderId: "order-1", ExpectedReadyAt: timestamppb.New(expected),
	})

	assert.Equal(t, []fulfillment.Notification{{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationDelayed, OccurredAt: occurredAt, ExpectedReadyAt: expected,
	}}, c.workflow.Notifications())
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

	assert.Equal(t, ordermessaging.RestaurantOrderEventsTopic, c.poisoned(t).Metadata.Get(messaging.TopicDeadLetteredMetadata))
	assert.Empty(t, c.workflow.Notifications())
}

func TestInvalidCommandGoesToDLQ(t *testing.T) {
	c := startConsumer(t)

	c.publish(t, ordermessaging.RestaurantOrderEventsTopic, "event-1", &restaurantsv1.PreparationDelayed{OrderId: "order-1"})

	assert.Equal(t, "event-1", c.poisoned(t).UUID)
}
