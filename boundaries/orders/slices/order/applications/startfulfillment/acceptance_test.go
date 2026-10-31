package startfulfillment_test

import (
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/startfulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestAcceptance runs acceptance/startfulfillment.feature (ADR 19). A scenario is a subtest;
// its mocks report to it through godog.T. The store and the cleanup of the mocks
// belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	windowStart := time.Date(2026, time.October, 27, 19, 0, 0, 0, time.UTC)
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				store    *repositorytest.Store
				workflow *MockStarter
				handler  startfulfillment.Handler
				result   startfulfillment.Result
				repeated startfulfillment.Result
				failure  error
			)

			sc.Given(`^an order is placed$`, func(ctx context.Context) {
				store, workflow, handler = newHandler(t)
				workflow.Test(godog.T(ctx))
			})
			sc.Given(`^an order is (completed|cancelled)$`, func(ctx context.Context, state string) {
				store, workflow, handler = newHandler(t, ordertest.WithState(order.State(state)))
				workflow.Test(godog.T(ctx))
			})
			sc.Given(`^a scheduled order is placed$`, func(ctx context.Context) {
				store, workflow, handler = newHandler(t, ordertest.WithDeliveryWindow(t, windowStart, windowStart.Add(15*time.Minute)))
				workflow.Test(godog.T(ctx))
			})
			sc.Given(`^the placement of an order has been received$`, func(ctx context.Context) {
				store, workflow, handler = newHandler(t)
				workflow.Test(godog.T(ctx))
				workflow.EXPECT().Start(mock.Anything, started).Return(nil).Once()
				_, err := handler.Handle(ctx, input())
				require.NoError(godog.T(ctx), err)
			})

			sc.When(`^the placement of the order is received$`, func(ctx context.Context) {
				workflow.EXPECT().Start(mock.Anything, started).Return(nil).Once()
				var err error
				result, err = handler.Handle(ctx, input())
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the same placement is received again$`, func(ctx context.Context) {
				workflow.EXPECT().Start(mock.Anything, started).Return(nil).Once()
				var err error
				result, err = handler.Handle(ctx, input())
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the placement of the final order is received twice$`, func(ctx context.Context) {
				var err error
				result, err = handler.Handle(ctx, input())
				require.NoError(godog.T(ctx), err)
				repeated, err = handler.Handle(ctx, input())
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the placement of an unknown order is received$`, func(ctx context.Context) {
				in := input()
				in.OrderID = "order-unknown"
				_, failure = handler.Handle(ctx, in)
			})
			sc.When(`^the placement of the scheduled order is received$`, func(ctx context.Context) {
				window, err := order.NewDeliveryWindow(windowStart, windowStart.Add(15*time.Minute))
				require.NoError(godog.T(ctx), err)
				scheduled := started
				scheduled.DeliveryWindow = window
				workflow.EXPECT().Start(mock.Anything, scheduled).Return(nil).Once()
				in := input()
				in.DeliveryWindowStart, in.DeliveryWindowEnd = windowStart, windowStart.Add(15*time.Minute)
				result, err = handler.Handle(ctx, in)
				require.NoError(godog.T(ctx), err)
			})

			sc.Then(`^order fulfillment is started once$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Start", 1)
			})
			sc.Then(`^the placement counts as processed$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeApplied, result.Outcome)
				assert.Equal(godog.T(ctx), 1, store.InboxSize())
			})
			sc.Then(`^order fulfillment is started again$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Start", 2)
			})
			sc.Then(`^the repeated placement counts as a duplicate$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeDuplicate, result.Outcome)
				assert.Equal(godog.T(ctx), 1, store.InboxSize())
			})
			sc.Then(`^order fulfillment is not started$`, func(ctx context.Context) {
				workflow.AssertNotCalled(godog.T(ctx), "Start", mock.Anything, mock.Anything)
			})
			sc.Then(`^the placement counts as late both times$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), startfulfillment.Result{Outcome: fulfillment.OutcomeIgnoredLate}, result)
				assert.Equal(godog.T(ctx), startfulfillment.Result{Outcome: fulfillment.OutcomeIgnoredLate}, repeated)
				assert.Equal(godog.T(ctx), 1, store.InboxSize())
			})
			sc.Then(`^the order is reported as not found$`, func(ctx context.Context) {
				assert.ErrorIs(godog.T(ctx), failure, order.ErrOrderNotFound)
			})
			sc.Then(`^order fulfillment is started with the delivery window$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
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
