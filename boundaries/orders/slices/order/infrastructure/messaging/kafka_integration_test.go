//go:build integration

package messaging_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/ThreeDotsLabs/watermill"
	watermillkafka "github.com/ThreeDotsLabs/watermill-kafka/v3/pkg/kafka"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recorddeliverycompletion"
	"nomnom/orders/slices/order/applications/recorddeliveryfailure"
	"nomnom/orders/slices/order/applications/recordorderready"
	"nomnom/orders/slices/order/applications/recordpickup"
	"nomnom/orders/slices/order/applications/recordpreparationstarted"
	"nomnom/orders/slices/order/applications/rejectorder"
	"nomnom/orders/slices/order/applications/relaypreparationissue"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
	ordermessaging "nomnom/orders/slices/order/infrastructure/messaging"
	deliveryv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/delivery/v1"
	restaurantsv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/restaurants/v1"
)

// flakyWorkflow fails the first failures notifications of selected Orders, as a
// temporarily unavailable Temporal does.
type flakyWorkflow struct {
	*memory.Workflow
	mu       sync.Mutex
	failures map[order.ID]int
}

func (w *flakyWorkflow) Notify(ctx context.Context, n fulfillment.Notification) error {
	w.mu.Lock()
	if w.failures[n.OrderID] > 0 {
		w.failures[n.OrderID]--
		w.mu.Unlock()
		return errors.New("temporal unavailable")
	}
	w.mu.Unlock()
	return w.Workflow.Notify(ctx, n)
}

func (w *flakyWorkflow) kindsOf(id order.ID) []fulfillment.Kind {
	var result []fulfillment.Kind
	for _, n := range w.Notifications() {
		if n.OrderID == id {
			result = append(result, n.Kind)
		}
	}
	return result
}

// TestKafkaConsumerAgainstKafka: Kafka → Watermill → EventGroupProcessor → Inbox →
// handler на реальном брокере: ack после commit, повторная доставка, порядок по
// ключу aggregate_id в топике с несколькими partitions, retry, DLQ стрима и битый record, который не блокирует partition.
func TestKafkaConsumerAgainstKafka(t *testing.T) {
	ctx := context.Background()
	brokers := kafka.Brokers

	cfg := config.KafkaConfig{
		Brokers:       brokers,
		ClientID:      "orders-consumer-test",
		ConsumerGroup: "orders",
		InitialOffset: config.KafkaOffsetOldest,
	}
	createTopics(t, brokers,
		ordermessaging.RestaurantOrderEventsTopic, messaging.DeadLetterTopic(ordermessaging.RestaurantOrderEventsTopic),
		ordermessaging.DeliveryEventsTopic, messaging.DeadLetterTopic(ordermessaging.DeliveryEventsTopic))

	store := memory.NewStore()
	for _, o := range []*order.Order{
		ordertest.New(t, "order-accepted"),
		ordertest.New(t, "order-retry"),
		ordertest.New(t, "order-after-broken"),
		ordertest.New(t, "order-b", ordertest.WithState(order.StateConfirmed)),
		ordertest.New(t, "order-c", ordertest.WithState(order.StateConfirmed)),
	} {
		require.NoError(t, store.Add(ctx, o))
	}
	workflow := &flakyWorkflow{Workflow: memory.NewWorkflow(), failures: map[order.ID]int{"order-retry": 2}}

	logger := watermill.NopLogger{}
	subscriber, err := messaging.NewSubscriber(cfg, logger)
	require.NoError(t, err)
	poisonPublisher, err := messaging.NewDeadLetterPublisher(cfg, logger)
	require.NoError(t, err)
	router, err := messaging.NewConsumerRouter(messaging.ConsumerConfig{
		ConsumerGroup: cfg.ConsumerGroup,
		Retry:         messaging.RetryPolicy{MaxRetries: 3, InitialInterval: 10 * time.Millisecond, MaxInterval: 50 * time.Millisecond},
		CloseTimeout:  10 * time.Second,
	}, subscriber, poisonPublisher, ordermessaging.Streams(handlersOf(store, workflow)), logger)
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(ctx)
	go func() { _ = router.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		_ = router.Close()
		_ = poisonPublisher.Close()
	})
	<-router.Running()

	poison := consumePoison(t, cfg)
	publish := publisher(t, brokers)

	t.Run("event is applied once and redelivery is a duplicate", func(t *testing.T) {
		accepted := &restaurantsv1.RestaurantOrderAccepted{OrderId: "order-accepted"}
		publish(ordermessaging.RestaurantOrderEventsTopic, "kitchen-accepted", "event-accepted", accepted)
		eventually(t, func() bool { return state(store, "order-accepted") == order.StateConfirmed })

		publish(ordermessaging.RestaurantOrderEventsTopic, "kitchen-accepted", "event-accepted", accepted)
		eventually(t, func() bool { return len(workflow.kindsOf("order-accepted")) == 2 })
		assert.Equal(t, order.StateConfirmed, state(store, "order-accepted"))
		assert.Equal(t, 1, store.InboxSize(), "redelivery is not recorded twice")
	})

	t.Run("events of one aggregate keep their order across partitions", func(t *testing.T) {
		for _, step := range []struct {
			eventID string
			event   func(id string) proto.Message
		}{
			{"assigned", func(id string) proto.Message { return &deliveryv1.CourierAssignedToDelivery{OrderId: id} }},
			{"picked-up", func(id string) proto.Message { return &deliveryv1.DeliveryPickedUp{OrderId: id} }},
			{"completed", func(id string) proto.Message { return &deliveryv1.DeliveryCompleted{OrderId: id} }},
		} {
			for _, id := range []string{"order-b", "order-c"} {
				publish(ordermessaging.DeliveryEventsTopic, "delivery-"+id, id+"-"+step.eventID, step.event(id))
			}
		}
		want := []fulfillment.Kind{fulfillment.KindCourierAssigned, fulfillment.KindPickedUp, fulfillment.KindDeliveryCompleted}
		eventually(t, func() bool {
			return len(workflow.kindsOf("order-b")) == 3 && len(workflow.kindsOf("order-c")) == 3
		})
		assert.Equal(t, want, workflow.kindsOf("order-b"))
		assert.Equal(t, want, workflow.kindsOf("order-c"))
	})

	t.Run("transient failure is retried until applied", func(t *testing.T) {
		publish(ordermessaging.RestaurantOrderEventsTopic, "kitchen-retry", "event-retry", &restaurantsv1.RestaurantOrderReady{OrderId: "order-retry"})

		eventually(t, func() bool { return len(workflow.kindsOf("order-retry")) == 1 })
	})

	t.Run("broken record goes to the DLQ without blocking its partition", func(t *testing.T) {
		raw(t, brokers, ordermessaging.RestaurantOrderEventsTopic, "kitchen-broken", []byte{0xff, 0x00})
		publish(ordermessaging.RestaurantOrderEventsTopic, "kitchen-broken", "event-after-broken", &restaurantsv1.RestaurantOrderReady{OrderId: "order-after-broken"})

		select {
		case msg := <-poison:
			assert.NotEmpty(t, msg.Metadata.Get(messaging.DeserializationErrorMetadata))
			assert.Equal(t, []byte{0xff, 0x00}, []byte(msg.Payload))
		case <-time.After(30 * time.Second):
			t.Fatal("broken record is not in the DLQ")
		}
		eventually(t, func() bool { return len(workflow.kindsOf("order-after-broken")) == 1 })
	})

	t.Run("unknown event type goes to the DLQ", func(t *testing.T) {
		publish(ordermessaging.RestaurantOrderEventsTopic, "kitchen-x", "event-unknown", &deliveryv1.DeliveryPickedUp{OrderId: "order-accepted"})

		select {
		case msg := <-poison:
			assert.Equal(t, "event-unknown", msg.UUID)
			assert.Equal(t, ordermessaging.RestaurantOrderEventsTopic, msg.Metadata.Get(messaging.TopicDeadLetteredMetadata))
		case <-time.After(30 * time.Second):
			t.Fatal("no message in DLQ")
		}
	})

	select {
	case msg := <-poison:
		t.Fatalf("unexpected dead letter %s: %s", msg.UUID, msg.Metadata.Get(messaging.ReasonDeadLetteredMetadata))
	default:
	}
}

func handlersOf(store *memory.Store, workflow fulfillment.Notifier) ordermessaging.Handlers {
	return ordermessaging.Handlers{
		ConfirmOrder:             confirmorder.NewHandler(store, store, store, workflow),
		RejectOrder:              rejectorder.NewHandler(store, store, store, workflow),
		RecordPreparationStarted: recordpreparationstarted.NewHandler(store, store, store, workflow),
		RecordOrderReady:         recordorderready.NewHandler(store, store, store, workflow),
		RelayPreparationIssue:    relaypreparationissue.NewHandler(workflow),
		RecordCourierAssignment:  recordcourierassignment.NewHandler(store, store, store, workflow),
		RecordPickup:             recordpickup.NewHandler(store, store, store, workflow),
		RecordDeliveryCompletion: recorddeliverycompletion.NewHandler(store, store, store, workflow),
		RecordDeliveryFailure:    recorddeliveryfailure.NewHandler(store, store, store, workflow),
	}
}

func createTopics(t *testing.T, brokers []string, topics ...string) {
	t.Helper()
	admin, err := sarama.NewClusterAdmin(brokers, sarama.NewConfig())
	require.NoError(t, err)
	defer func() { _ = admin.Close() }()
	for _, topic := range topics {
		require.NoError(t, admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 3, ReplicationFactor: 1}, false))
	}
}

// publisher publishes Integration Events as publishers of other contexts do:
// envelope in headers, Protobuf payload, partition key aggregate_id.
func publisher(t *testing.T, brokers []string) func(topic, aggregateID, eventID string, event proto.Message) {
	t.Helper()
	pub, err := watermillkafka.NewPublisher(watermillkafka.PublisherConfig{
		Brokers: brokers,
		Marshaler: watermillkafka.NewWithPartitioningMarshaler(func(_ string, msg *message.Message) (string, error) {
			return msg.Metadata.Get(messaging.AggregateIDMetadata), nil
		}),
	}, watermill.NopLogger{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() })

	occurredAt := time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)
	return func(topic, aggregateID, eventID string, event proto.Message) {
		t.Helper()
		msg, err := messaging.NewIntegrationEventMessage(messaging.Envelope{
			EventID:          eventID,
			OccurredAt:       occurredAt,
			AggregateType:    "source",
			AggregateID:      aggregateID,
			AggregateVersion: 1,
		}, event)
		require.NoError(t, err)
		require.NoError(t, pub.Publish(topic, msg))
	}
}

// raw produces a record as a broken publisher would: no envelope headers.
func raw(t *testing.T, brokers []string, topic, key string, value []byte) {
	t.Helper()
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer(brokers, cfg)
	require.NoError(t, err)
	defer func() { _ = producer.Close() }()
	_, _, err = producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(value)})
	require.NoError(t, err)
}

func consumePoison(t *testing.T, cfg config.KafkaConfig) <-chan *message.Message {
	t.Helper()
	cfg.ConsumerGroup = "poison-reader"
	subscriber, err := messaging.NewSubscriber(cfg, watermill.NopLogger{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscriber.Close() })
	messages, err := subscriber.Subscribe(context.Background(), messaging.DeadLetterTopic(ordermessaging.RestaurantOrderEventsTopic))
	require.NoError(t, err)

	poison := make(chan *message.Message, 10)
	go func() {
		for msg := range messages {
			msg.Ack()
			poison <- msg
		}
	}()
	return poison
}

func state(store *memory.Store, id order.ID) order.State {
	snapshot, _ := store.Snapshot(id)
	return snapshot.State
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	require.Eventually(t, condition, 60*time.Second, 50*time.Millisecond)
}
