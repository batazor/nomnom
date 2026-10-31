package confirmorder_test

import (
	"context"
	"testing"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/confirmorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestAcceptance runs acceptance/confirmorder.feature (ADR 19); @AC-001 also
// covers the placed Order of AC-108. A scenario is a subtest; its mocks report
// to it through godog.T. The store and the cleanup of the mocks belong to t:
// godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				handler     confirmorder.Handler
				store       *repositorytest.Store
				workflow    *MockNotifier
				result      confirmorder.Result
				redelivered confirmorder.Result
				republished confirmorder.Result
			)

			sc.Given(`^an order is waiting for restaurant confirmation$`, func(ctx context.Context) {
				handler, store, workflow = setup(t, order.StatePlaced)
				workflow.Test(godog.T(ctx))
			})
			sc.Given(`^an order has already recorded the restaurant's acceptance$`, func(ctx context.Context) {
				handler, store, workflow = setup(t, order.StatePlaced)
				workflow.Test(godog.T(ctx))
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				_, err := handler.Handle(ctx, input("event-1"))
				require.NoError(godog.T(ctx), err)
			})

			sc.When(`^the restaurant accepts the order$`, func(ctx context.Context) {
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				var err error
				result, err = handler.Handle(ctx, input("event-1"))
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the restaurant accepts the order again$`, func(ctx context.Context) {
				// Every delivery notifies the workflow; the workflow deduplicates by event_id.
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-2", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				var err error
				redelivered, err = handler.Handle(ctx, input("event-1"))
				require.NoError(godog.T(ctx), err)
				republished, err = handler.Handle(ctx, input("event-2"))
				require.NoError(godog.T(ctx), err)
			})

			sc.Then(`^the order records the restaurant's acceptance$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeApplied, result.Outcome)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StateConfirmed, snapshot.State)
				assert.Empty(godog.T(ctx), store.OutboxRecords())
			})
			sc.Then(`^order fulfillment can continue$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Notify", 1)
			})
			sc.Then(`^the order still records one restaurant acceptance$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StateConfirmed, snapshot.State)
				assert.Equal(godog.T(ctx), []order.AppliedTransition{{Trigger: order.TriggerConfirm, OccurredAt: occurredAt}}, snapshot.Transitions)
			})
			sc.Then(`^no additional business effect occurs$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeDuplicate, redelivered.Outcome)
				assert.Equal(godog.T(ctx), fulfillment.OutcomeAlreadyApplied, republished.Outcome)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), uint64(2), snapshot.Version, "only the first event saves the Order")
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
