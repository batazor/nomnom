package observability_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/slices/order/applications/closure/requestcancellation"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// testReasons stands in for the reason codes of the fulfillment process.
var testReasons = observability.ReasonCodes{"payment_declined", "customer_requested"}

func installMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return reader
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	out := map[string]metricdata.Aggregation{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m.Data
		}
	}
	return out
}

// sums — значения counter по значению attribute (пустой key — без attributes).
func sums(t *testing.T, data metricdata.Aggregation, key string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	if data == nil {
		return out
	}
	sum, ok := data.(metricdata.Sum[int64])
	require.True(t, ok, "counter %T", data)
	for _, dp := range sum.DataPoints {
		value, _ := dp.Attributes.Value(attribute.Key(key))
		out[value.AsString()] += dp.Value
	}
	return out
}

func cancelled(t *testing.T, outcome order.NegativeOutcome, code string) order.OrderCancelled {
	t.Helper()
	reason, err := order.NewCancellationReason(outcome, code)
	require.NoError(t, err)
	return order.OrderCancelled{OrderID: "order-1", Reason: reason}
}

// commit имитирует ObservedStore.Do: events попадают в Outbox внутри fn.
func commit(ctx context.Context, m *observability.DomainMetrics, fnErr error, events ...order.DomainEvent) {
	ctx, attempt, done := m.Track(ctx)
	attempt()
	m.Appended(ctx, events)
	done(fnErr)
}

func TestDomainMetricsCountCommittedEventsOnly(t *testing.T) {
	reader := installMeter(t)
	m := observability.NewDomainMetrics(testReasons)
	ctx := context.Background()

	commit(ctx, m, nil, order.OrderPlaced{OrderID: "order-1"})
	commit(ctx, m, errors.New("rollback"), order.OrderPlaced{OrderID: "order-2"})
	commit(ctx, m, nil, order.OrderCompleted{OrderID: "order-1", PlacedAt: time.Now().Add(-time.Hour)})

	got := collect(t, reader)
	assert.Equal(t, map[string]int64{"": 1}, sums(t, got["nomnom.orders.placed"], ""), "rollback не считается")
	assert.Equal(t, map[string]int64{"": 1}, sums(t, got["nomnom.orders.completed"], ""))
	histogram, ok := got["nomnom.orders.fulfillment.duration"].(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, histogram.DataPoints, 1)
	assert.Equal(t, uint64(1), histogram.DataPoints[0].Count)
	assert.InDelta(t, 3600, histogram.DataPoints[0].Sum, 5)
}

func TestNestedUnitOfWorkRecordsOnceAfterOuterCommit(t *testing.T) {
	reader := installMeter(t)
	m := observability.NewDomainMetrics(testReasons)

	outer, _, done := m.Track(context.Background())
	inner, _, innerDone := m.Track(outer)
	m.Appended(inner, []order.DomainEvent{order.OrderPlaced{OrderID: "order-1"}})
	innerDone(nil)
	assert.Empty(t, sums(t, collect(t, reader)["nomnom.orders.placed"], ""), "вложенный Do не фиксирует")

	done(nil)
	assert.Equal(t, map[string]int64{"": 1}, sums(t, collect(t, reader)["nomnom.orders.placed"], ""))
}

func TestAppendOutsideUnitOfWorkIsRecordedImmediately(t *testing.T) {
	reader := installMeter(t)
	m := observability.NewDomainMetrics(testReasons)

	m.Appended(context.Background(), []order.DomainEvent{order.OrderPlaced{OrderID: "order-1"}})

	assert.Equal(t, map[string]int64{"": 1}, sums(t, collect(t, reader)["nomnom.orders.placed"], ""))
}

func TestCancellationsSplitByOutcomeWithClosedReasons(t *testing.T) {
	reader := installMeter(t)
	m := observability.NewDomainMetrics(testReasons)

	commit(context.Background(), m, nil,
		cancelled(t, order.OutcomeCancellation, "customer_requested"),
		cancelled(t, order.OutcomeFailure, "payment_declined"),
		cancelled(t, order.OutcomeFailure, "made_up_reason"),
	)

	got := collect(t, reader)
	assert.Equal(t, map[string]int64{"customer_requested": 1}, sums(t, got["nomnom.orders.cancelled"], "nomnom.reason"))
	assert.Equal(t, map[string]int64{"payment_declined": 1, "other": 1}, sums(t, got["nomnom.orders.failed"], "nomnom.reason"))
}

func TestCancellationRequestsCountAnswersAndRejectionsNotFailures(t *testing.T) {
	reader := installMeter(t)
	m := observability.NewDomainMetrics(testReasons)
	answer := func(result requestcancellation.Result, err error) {
		handle := observability.CancellationRequests(m, func(context.Context, requestcancellation.Input) (requestcancellation.Result, error) {
			return result, err
		})
		_, _ = handle(context.Background(), requestcancellation.Input{})
	}

	answer(requestcancellation.Result{Outcome: requestcancellation.OutcomeRequested}, nil)
	answer(requestcancellation.Result{Outcome: requestcancellation.OutcomeAlreadyCancelled}, nil)
	answer(requestcancellation.Result{}, authz.FailClosed(errors.New("denied")))
	answer(requestcancellation.Result{}, errors.New("connection reset"))

	assert.Equal(t, map[string]int64{"requested": 1, "already_cancelled": 1, "rejected": 1},
		sums(t, collect(t, reader)["nomnom.orders.cancellation_requests"], "nomnom.outcome"))
}

func TestOrdersInStateObservesStoreCounts(t *testing.T) {
	reader := installMeter(t)
	observability.ObserveOrdersInState(func(context.Context) (map[order.State]int64, error) {
		return map[order.State]int64{order.StatePlaced: 2, order.StateCompleted: 5}, nil
	})

	gauge, ok := collect(t, reader)["nomnom.orders.in_state"].(metricdata.Gauge[int64])
	require.True(t, ok)
	got := map[string]int64{}
	for _, dp := range gauge.DataPoints {
		state, _ := dp.Attributes.Value("nomnom.order.state")
		got[state.AsString()] = dp.Value
	}
	assert.Equal(t, map[string]int64{string(order.StatePlaced): 2, string(order.StateCompleted): 5}, got)
}
