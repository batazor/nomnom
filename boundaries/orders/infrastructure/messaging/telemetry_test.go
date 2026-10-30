package messaging_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"nomnom/orders/infrastructure/messaging"
)

// Metric names and attributes are a contract of ops/grafana/inventory (messaging).
func TestConsumerMetricsFollowDashboardInventory(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	calls := 0
	schemas := NewMockSchemaSubjects(t)
	schemas.EXPECT().Subjects(mock.Anything, schemaID).Return([]string{stringValue}, nil)
	h := start(t, schemas, func(ctx context.Context, e *wrapperspb.StringValue) error {
		calls++
		switch e.GetValue() {
		case "flaky":
			if calls == 1 {
				return errors.New("database unavailable")
			}
		case "duplicate":
			messaging.MarkDuplicate(ctx)
		case "broken":
			return messaging.Permanent(errors.New("order not found"))
		}
		return nil
	})
	h.publish(t, event(t, "event-1", wrapperspb.String("flaky")))
	h.publish(t, event(t, "event-2", wrapperspb.String("duplicate")))
	h.publish(t, event(t, "event-3", wrapperspb.String("broken")))
	h.poisoned(t)

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))
	got := map[string][]attribute.Set{}
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, p := range d.DataPoints {
					got[m.Name] = append(got[m.Name], p.Attributes)
				}
			case metricdata.Sum[int64]:
				for _, p := range d.DataPoints {
					got[m.Name] = append(got[m.Name], p.Attributes)
				}
			}
		}
	}

	for _, name := range []string{
		"nomnom.messaging.process.duration",
		"nomnom.messaging.message.age",
		"nomnom.messaging.retry.attempts",
		"nomnom.messaging.poison.messages",
		"nomnom.inbox.duplicates",
	} {
		require.NotEmpty(t, got[name], name)
		for _, attrs := range got[name] {
			stream, _ := attrs.Value("messaging.destination.name")
			assert.Equal(t, topic, stream.AsString(), name)
			assert.True(t, attrs.HasValue("nomnom.messaging.handler"), name)
			assert.True(t, attrs.HasValue("nomnom.event.type"), name)
			assert.False(t, attrs.HasValue("event_id"), name)
			assert.False(t, attrs.HasValue("aggregate_id"), name)
		}
	}
	classes := map[string]bool{}
	for _, attrs := range got["nomnom.messaging.process.duration"] {
		class, ok := attrs.Value("nomnom.error.class")
		classes[class.AsString()] = ok
	}
	assert.Equal(t, map[string]bool{"": false, "permanent": true}, classes,
		"error_class is set only for failed messages; a retried success is a success")
}
