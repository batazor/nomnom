package complete_test

import (
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/closure/complete"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestAcceptance runs acceptance/complete.feature (ADR 19). The store belongs
// to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				handler complete.Handler
				store   *repositorytest.Store
				state   order.State
				result  complete.Result
				failure error
			)
			published := func() []repositorytest.OutboxRecord {
				return repositorytest.ExpectedOutbox(t, "order-1", 2,
					order.OrderCompleted{OrderID: "order-1", MarketID: ordertest.MarketID(t), PlacedAt: ordertest.PlacedAt, CompletedAt: completedAt},
				)
			}

			sc.Given(`^an order is (delivered|placed|picked_up|cancelled)$`, func(given string) {
				state = order.State(given)
				handler, store = setup(t, state)
			})
			sc.Given(`^an order has already been completed$`, func(ctx context.Context) {
				handler, store = setup(t, order.StateDelivered)
				_, err := handler.Handle(ctx, complete.Input{OrderID: "order-1", CompletedAt: completedAt})
				require.NoError(godog.T(ctx), err)
			})

			sc.When(`^order fulfillment completes the order$`, func(ctx context.Context) {
				var err error
				result, err = handler.Handle(ctx, complete.Input{OrderID: "order-1", CompletedAt: completedAt})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^order fulfillment completes the order again$`, func(ctx context.Context) {
				// A retry carries the same moment; even another one adds no event.
				var err error
				result, err = handler.Handle(ctx, complete.Input{OrderID: "order-1", CompletedAt: completedAt.Add(time.Minute)})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^order fulfillment tries to complete the order$`, func(ctx context.Context) {
				_, failure = handler.Handle(ctx, complete.Input{OrderID: "order-1", CompletedAt: completedAt})
			})

			sc.Then(`^the order is completed$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), complete.Result{Outcome: complete.OutcomeCompleted}, result)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StateCompleted, snapshot.State)
				assert.Equal(godog.T(ctx), uint64(2), snapshot.Version)
			})
			sc.Then(`^the completion is published with its moment$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), published(), store.OutboxRecords())
			})
			sc.Then(`^the order stays completed with one published completion$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), complete.Result{Outcome: complete.OutcomeAlreadyCompleted}, result)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StateCompleted, snapshot.State)
				assert.Equal(godog.T(ctx), uint64(2), snapshot.Version)
				assert.Equal(godog.T(ctx), published(), store.OutboxRecords())
			})
			sc.Then(`^the completion is refused$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), order.InvalidTransition{From: state, Trigger: order.TriggerComplete}, failure)
			})
			sc.Then(`^the order is not changed$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), state, snapshot.State)
				assert.Empty(godog.T(ctx), store.OutboxRecords())
			})
		},
		Options: &godog.Options{
			Format:   "progress",
			Paths:    []string{"acceptance"},
			Strict:   true,
			TestingT: t,
		},
	}

	require.Zero(t, suite.Run(), "acceptance scenarios failed")
}
