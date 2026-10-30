package messaging_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"nomnom/orders/infrastructure/messaging"
)

// Встроенные метрики Watermill consumer-роли пишутся в переданный registry под
// namespace watermill и subsystem consumer, включая publish в DLQ.
func TestWatermillBuiltinMetricsUseConsumerSubsystem(t *testing.T) {
	registry := prometheus.NewRegistry()
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	pubSub := gochannel.NewGoChannel(gochannel.Config{BlockPublishUntilSubscriberAck: true}, watermill.NopLogger{})
	dlq, err := pubSub.Subscribe(context.Background(), messaging.DeadLetterTopic(topic))
	require.NoError(t, err)
	go func() {
		for msg := range dlq {
			msg.Ack()
		}
	}()

	router, err := messaging.NewConsumerRouter(messaging.ConsumerConfig{
		ConsumerGroup: "orders",
		Retry:         messaging.RetryPolicy{MaxRetries: 0, InitialInterval: time.Millisecond, MaxInterval: time.Millisecond},
		Schemas:       schemas,
		Prometheus:    registry,
	}, pubSub, pubSub, []messaging.Stream{{
		Group: "test",
		Topic: topic,
		Handlers: []cqrs.GroupEventHandler{cqrs.NewGroupEventHandler(func(_ context.Context, e *wrapperspb.StringValue) error {
			if e.GetValue() == "bad" {
				return messaging.Permanent(errors.New("invalid"))
			}
			return nil
		})},
	}}, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = router.Run(ctx) }()
	t.Cleanup(func() { cancel(); _ = router.Close() })
	<-router.Running()

	h := harness{pubSub: pubSub}
	h.publish(t, event(t, "event-1", wrapperspb.String("ok")))
	h.publish(t, event(t, "event-2", wrapperspb.String("bad")))

	// Watermill увеличивает subscriber_messages_received_total в горутине после
	// Ack, то есть уже после возврата publish: ждём появления всех метрик.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		families, err := registry.Gather()
		require.NoError(c, err)
		names := map[string]bool{}
		for _, f := range families {
			names[f.GetName()] = true
		}
		for _, want := range []string{
			"watermill_consumer_subscriber_messages_received_total",
			"watermill_consumer_handler_execution_time_seconds",
			"watermill_consumer_publish_time_seconds",
		} {
			assert.True(c, names[want], "%s not in %v", want, names)
		}
	}, 5*time.Second, 10*time.Millisecond)
}
