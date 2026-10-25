//go:build integration

package messaging_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/ThreeDotsLabs/watermill"
	watermillkafka "github.com/ThreeDotsLabs/watermill-kafka/v3/pkg/kafka"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
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

// Тот же образ, что Kafka в ops/kind/manifests/messaging/infrastructure.yaml.
const kafkaImage = "apache/kafka:4.4.0"

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
// ключу aggregate_id в топике с несколькими partitions, retry и poison topic.
func TestKafkaConsumerAgainstKafka(t *testing.T) {
	ctx := context.Background()
	brokers := startKafka(t)

	cfg := config.KafkaConfig{
		Brokers:       brokers,
		ClientID:      "orders-consumer-test",
		ConsumerGroup: "orders",
		PoisonTopic:   "orders.v1.consumer-poison",
	}
	createTopics(t, brokers, ordermessaging.RestaurantOrderEventsTopic, ordermessaging.DeliveryEventsTopic, cfg.PoisonTopic)

	store := memory.NewStore()
	for _, o := range []*order.Order{
		ordertest.New(t, "order-accepted"),
		ordertest.New(t, "order-retry"),
		ordertest.New(t, "order-b", ordertest.WithState(order.StateConfirmed)),
		ordertest.New(t, "order-c", ordertest.WithState(order.StateConfirmed)),
	} {
		require.NoError(t, store.Add(ctx, o))
	}
	workflow := &flakyWorkflow{Workflow: memory.NewWorkflow(), failures: map[order.ID]int{"order-retry": 2}}

	logger := watermill.NopLogger{}
	subscriber, err := messaging.NewSubscriber(cfg, logger)
	require.NoError(t, err)
	poisonPublisher, err := messaging.NewPoisonPublisher(cfg, logger)
	require.NoError(t, err)
	router, err := messaging.NewConsumerRouter(messaging.ConsumerConfig{
		PoisonTopic:  cfg.PoisonTopic,
		Retry:        messaging.RetryPolicy{MaxRetries: 3, InitialInterval: 10 * time.Millisecond, MaxInterval: 50 * time.Millisecond},
		CloseTimeout: 10 * time.Second,
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

	t.Run("unknown event type goes to the poison topic", func(t *testing.T) {
		publish(ordermessaging.RestaurantOrderEventsTopic, "kitchen-x", "event-unknown", &deliveryv1.DeliveryPickedUp{OrderId: "order-accepted"})

		select {
		case msg := <-poison:
			assert.Equal(t, "event-unknown", msg.UUID)
			assert.Equal(t, ordermessaging.RestaurantOrderEventsTopic, msg.Metadata.Get("topic_poisoned"))
		case <-time.After(30 * time.Second):
			t.Fatal("no message in poison topic")
		}
	})

	select {
	case msg := <-poison:
		t.Fatalf("unexpected poison message %s: %s", msg.UUID, msg.Metadata.Get("reason_poisoned"))
	default:
	}
}

// startKafka runs a single-node KRaft broker. The advertised listener must be the
// host-mapped address, so the host port is chosen before the container starts.
func startKafka(t *testing.T) []string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	broker := fmt.Sprintf("127.0.0.1:%d", port)
	kafka, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        kafkaImage,
			ExposedPorts: []string{"9092/tcp"},
			Env: map[string]string{
				"KAFKA_NODE_ID":                                  "1",
				"KAFKA_PROCESS_ROLES":                            "broker,controller",
				"KAFKA_LISTENERS":                                "PLAINTEXT://:9092,CONTROLLER://:9093",
				"KAFKA_ADVERTISED_LISTENERS":                     "PLAINTEXT://" + broker,
				"KAFKA_CONTROLLER_LISTENER_NAMES":                "CONTROLLER",
				"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":           "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
				"KAFKA_CONTROLLER_QUORUM_VOTERS":                 "1@localhost:9093",
				"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1",
				"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
				"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1",
				"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS":         "0",
			},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.PortBindings = network.PortMap{
					network.MustParsePort("9092/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: fmt.Sprint(port)}},
				}
			},
			WaitingFor: wait.ForLog("Kafka Server started").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, kafka)
	require.NoError(t, err)
	return []string{broker}
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

func consumePoison(t *testing.T, cfg config.KafkaConfig) <-chan *message.Message {
	t.Helper()
	cfg.ConsumerGroup = "poison-reader"
	subscriber, err := messaging.NewSubscriber(cfg, watermill.NopLogger{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscriber.Close() })
	messages, err := subscriber.Subscribe(context.Background(), cfg.PoisonTopic)
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
