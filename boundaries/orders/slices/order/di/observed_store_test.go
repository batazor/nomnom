package di_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"nomnom/orders/slices/order/applications/completeorder"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// Повтор Activity Complete Order после commit не создаёт событий и не
// увеличивает nomnom.orders.completed второй раз.
func TestObservedStoreCountsRetriedCompletionOnce(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	mem := memory.NewStore()
	require.NoError(t, mem.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed))))
	store := orderdi.ObservedStore{Store: mem, Metrics: observability.NewDomainMetrics(nil)}
	handler := completeorder.NewHandler(store, store, store)

	for range 2 {
		_, err := handler.Handle(context.Background(), completeorder.Input{OrderID: "order-1"})
		require.NoError(t, err)
	}

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var completed int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "nomnom.orders.completed" {
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					completed += dp.Value
				}
			}
		}
	}
	assert.Equal(t, int64(1), completed)
}
