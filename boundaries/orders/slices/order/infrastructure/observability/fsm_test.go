package observability_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

func transitionSet(from, to order.State, trigger order.Trigger, result string) attribute.Distinct {
	set := attribute.NewSet(
		attribute.String("nomnom.fsm.aggregate_type", "order"),
		attribute.String("nomnom.fsm.from_state", string(from)),
		attribute.String("nomnom.fsm.to_state", string(to)),
		attribute.String("nomnom.fsm.trigger", string(trigger)),
		attribute.String("nomnom.fsm.result", result),
		attribute.Bool("nomnom.fsm.terminal", to.IsFinal()),
	)
	return set.Equivalent()
}

// transitions — значения nomnom.fsm.transitions по полному набору attributes.
func transitions(t *testing.T, data metricdata.Aggregation) map[attribute.Distinct]int64 {
	t.Helper()
	out := map[attribute.Distinct]int64{}
	if data == nil {
		return out
	}
	sum, ok := data.(metricdata.Sum[int64])
	require.True(t, ok, "counter %T", data)
	assert.True(t, sum.IsMonotonic)
	for _, dp := range sum.DataPoints {
		out[dp.Attributes.Equivalent()] += dp.Value
	}
	return out
}

func observedStore(t *testing.T) (observability.ObservedStore, *repositorytest.Store) {
	t.Helper()
	pg := repositorytest.Open(t)
	return observability.ObservedStore{UnitOfWork: pg, Orders: pg, Outbox: pg, Metrics: observability.NewDomainMetrics(nil)}, pg
}

// Имя и attributes — контракт ops/grafana/inventory (раздел fsm): применённый
// переход считается после commit один раз, повтор в том же State — не переход,
// недопустимый переход — rejected без смены State.
func TestObservedStoreCountsFSMTransitions(t *testing.T) {
	reader := installMeter(t)
	store, pg := observedStore(t)
	ctx := context.Background()
	require.NoError(t, pg.Add(ctx, ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed))))
	complete := completeorder.NewHandler(store, store, store)
	finalize := failorcancelorder.NewHandler(store, store, store)
	completedAt := ordertest.PlacedAt.Add(time.Hour)

	for range 2 {
		_, err := complete.Handle(ctx, completeorder.Input{OrderID: "order-1", CompletedAt: completedAt})
		require.NoError(t, err)
	}
	_, err := finalize.Handle(ctx, failorcancelorder.Input{
		OrderID: "order-1", Outcome: order.OutcomeCancellation, ReasonCode: "customer_requested", DecidedAt: completedAt,
	})
	var invalid order.InvalidTransition
	require.ErrorAs(t, err, &invalid)

	got := transitions(t, collect(t, reader)["nomnom.fsm.transitions"])
	assert.Equal(t, map[attribute.Distinct]int64{
		transitionSet(order.StateConfirmed, order.StateCompleted, order.TriggerComplete, "applied"): 1,
		transitionSet(order.StateCompleted, order.StateCompleted, order.TriggerCancel, "rejected"):  1,
	}, got)
}

// Попытка, откатанная Unit of Work и повторённая им, не считает переход дважды;
// rollback без повтора не считает его вовсе.
func TestObservedStoreCountsTransitionOfRetriedAttemptOnce(t *testing.T) {
	reader := installMeter(t)
	store, pg := observedStore(t)
	ctx := context.Background()
	require.NoError(t, pg.Add(ctx, ordertest.New(t, "order-1")))
	require.NoError(t, pg.Add(ctx, ordertest.New(t, "order-2")))
	confirm := func(id order.ID, fail func(attempt int) error) error {
		attempt := 0
		return store.Do(ctx, func(ctx context.Context) error {
			attempt++
			current, err := store.Get(ctx, id)
			if err != nil {
				return err
			}
			if _, err := current.Confirm(ordertest.PlacedAt); err != nil {
				return err
			}
			if err := store.Save(ctx, current); err != nil {
				return err
			}
			return fail(attempt)
		})
	}

	require.NoError(t, confirm("order-1", func(attempt int) error {
		if attempt == 1 {
			return fmt.Errorf("stale read: %w", usecase.ErrConcurrencyConflict)
		}
		return nil
	}))
	require.Error(t, confirm("order-2", func(int) error { return fmt.Errorf("use case failed") }))

	got := transitions(t, collect(t, reader)["nomnom.fsm.transitions"])
	assert.Equal(t, map[attribute.Distinct]int64{
		transitionSet(order.StatePlaced, order.StateConfirmed, order.TriggerConfirm, "applied"): 1,
	}, got)
}
