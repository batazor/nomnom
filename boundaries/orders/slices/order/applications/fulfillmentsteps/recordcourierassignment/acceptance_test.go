package recordcourierassignment_test

import (
	"context"
	"testing"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordcourierassignment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestAcceptance runs acceptance/recordcourierassignment.feature (ADR 19);
// @AC-002 also covers the placed Order of AC-110. A scenario is a subtest; its
// mocks report to it through godog.T. The store and the cleanup of the mocks
// belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				handler  recordcourierassignment.Handler
				store    *repositorytest.Store
				workflow *MockNotifier
				result   recordcourierassignment.Result
			)

			sc.Given(`^an order is waiting for courier assignment$`, func(ctx context.Context) {
				store = repositorytest.Open(t)
				workflow = NewMockNotifier(t)
				workflow.Test(godog.T(ctx))
				require.NoError(godog.T(ctx), store.Add(ctx, ordertest.New(t, "order-1")))
				handler = recordcourierassignment.NewHandler(store, store, store, workflow, store, store)
			})

			sc.When(`^a courier is assigned to the delivery$`, func(ctx context.Context) {
				workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
					OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: occurredAt,
				}).Return(nil).Once()
				var err error
				result, err = handler.Handle(ctx, assignment("event-1", "courier-1", 2, occurredAt))
				require.NoError(godog.T(ctx), err)
			})

			sc.Then(`^the order records the courier assignment$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeApplied, result.Outcome)
				stored, found, err := store.CourierAssignment(ctx, "order-1")
				require.NoError(godog.T(ctx), err)
				require.True(godog.T(ctx), found)
				assert.Equal(godog.T(ctx), order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: occurredAt, DeliveryVersion: 2}, stored)
				assert.Equal(godog.T(ctx), 1, store.InboxSize())
			})
			sc.Then(`^order fulfillment can continue$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Notify", 1)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StatePlaced, snapshot.State)
				assert.Empty(godog.T(ctx), snapshot.Transitions)
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
