package failorcancelorder_test

import (
	"context"
	"testing"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/closure/failorcancelorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestAcceptance runs acceptance/failorcancelorder.feature (ADR 19). The store
// belongs to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				handler failorcancelorder.Handler
				store   *repositorytest.Store
				in      failorcancelorder.Input
				result  failorcancelorder.Result
				failure error
			)

			sc.Given(`^an order is (placed|confirmed|completed)$`, func(state string) {
				handler, store = setup(t, order.State(state))
			})
			sc.Given(`^an order has already received a negative outcome$`, func(ctx context.Context) {
				handler, store = setup(t, order.StateConfirmed)
				in = failorcancelorder.Input{OrderID: "order-1", Outcome: order.OutcomeFailure, ReasonCode: "delivery_failed", DecidedAt: decidedAt}
				_, err := handler.Handle(ctx, in)
				require.NoError(godog.T(ctx), err)
			})

			sc.When(`^order fulfillment records a (failure|cancellation) with reason ([a-z_]+)$`, func(ctx context.Context, outcome, reason string) {
				in = failorcancelorder.Input{OrderID: "order-1", Outcome: order.NegativeOutcome(outcome), ReasonCode: reason, DecidedAt: decidedAt}
				var err error
				result, err = handler.Handle(ctx, in)
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^order fulfillment records the outcome again$`, func(ctx context.Context) {
				var err error
				result, err = handler.Handle(ctx, in)
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^order fulfillment tries to cancel the order$`, func(ctx context.Context) {
				_, failure = handler.Handle(ctx, failorcancelorder.Input{
					OrderID: "order-1", Outcome: order.OutcomeCancellation, ReasonCode: "customer_requested", DecidedAt: decidedAt,
				})
			})

			sc.Then(`^the order is cancelled with that outcome and reason$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), failorcancelorder.Result{Outcome: failorcancelorder.OutcomeCancelled}, result)
				reason, err := order.NewCancellationReason(in.Outcome, in.ReasonCode)
				require.NoError(godog.T(ctx), err)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StateCancelled, snapshot.State)
				assert.Equal(godog.T(ctx), reason, snapshot.CancellationReason)
				assert.Equal(godog.T(ctx), decidedAt, snapshot.CancelledAt)
			})
			sc.Then(`^the outcome is published$`, func(ctx context.Context) {
				reason, err := order.NewCancellationReason(in.Outcome, in.ReasonCode)
				require.NoError(godog.T(ctx), err)
				assert.Equal(godog.T(ctx), repositorytest.ExpectedOutbox(t, "order-1", 2,
					order.OrderCancelled{OrderID: "order-1", MarketID: ordertest.MarketID(t), RequestedAt: decidedAt, Reason: reason},
				), store.OutboxRecords())
			})
			sc.Then(`^the order reports the outcome as already recorded$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), failorcancelorder.Result{Outcome: failorcancelorder.OutcomeAlreadyCancelled}, result)
			})
			sc.Then(`^the outcome is published once$`, func(ctx context.Context) {
				assert.Len(godog.T(ctx), store.OutboxRecords(), 1)
			})
			sc.Then(`^the negative outcome is refused$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), order.InvalidTransition{From: order.StateCompleted, Trigger: order.TriggerCancel}, failure)
			})
			sc.Then(`^the completed order is not changed$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StateCompleted, snapshot.State)
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
