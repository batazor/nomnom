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
				state    order.State
			)
			given := func(ctx context.Context, options ...ordertest.Option) {
				store = repositorytest.Open(t)
				workflow = NewMockNotifier(t)
				workflow.Test(godog.T(ctx))
				require.NoError(godog.T(ctx), store.Add(ctx, ordertest.New(t, "order-1", options...)))
				handler = recordcourierassignment.NewHandler(store, store, store, workflow, store, store)
			}

			sc.Given(`^an order is waiting for courier assignment$`, func(ctx context.Context) {
				given(ctx)
			})
			sc.Given(`^an order has recorded a courier assignment$`, func(ctx context.Context) {
				given(ctx)
				workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
					OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: occurredAt,
				}).Return(nil).Once()
				_, err := handler.Handle(ctx, assignment("event-1", "courier-1", 1, occurredAt))
				require.NoError(godog.T(ctx), err)
			})
			sc.Given(`^an order is (completed|cancelled)$`, func(ctx context.Context, final string) {
				state = order.State(final)
				given(ctx, ordertest.WithState(state))
			})

			sc.When(`^a courier is assigned to the delivery$`, func(ctx context.Context) {
				workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
					OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: occurredAt,
				}).Return(nil).Once()
				var err error
				result, err = handler.Handle(ctx, assignment("event-1", "courier-1", 2, occurredAt))
				require.NoError(godog.T(ctx), err)
			})

			sc.When(`^the same assignment is delivered again$`, func(ctx context.Context) {
				workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
					OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: occurredAt,
				}).Return(nil).Once()
				var err error
				result, err = handler.Handle(ctx, assignment("event-1", "courier-1", 1, occurredAt))
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^an assignment arrives after order fulfillment is closed$`, func(ctx context.Context) {
				workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
					OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: occurredAt,
				}).Return(fulfillment.ErrWorkflowNotFound).Once()
				var err error
				result, err = handler.Handle(ctx, assignment("event-1", "courier-1", 1, occurredAt))
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
			sc.Then(`^the order does not change again$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeDuplicate, result.Outcome)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), uint64(2), snapshot.Version, "the duplicate must not save the Order")
				assert.Equal(godog.T(ctx), 1, store.InboxSize())
			})
			sc.Then(`^order fulfillment is told of the assignment again$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Notify", 2)
			})
			sc.Then(`^the order does not change$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), state, snapshot.State)
				assert.Empty(godog.T(ctx), snapshot.Transitions)
				assert.Equal(godog.T(ctx), uint64(1), snapshot.Version)
			})
			sc.Then(`^it counts as processed late$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), recordcourierassignment.Result{Outcome: fulfillment.OutcomeIgnoredLate}, result)
				assert.Equal(godog.T(ctx), 1, store.InboxSize(), "redelivery must become a duplicate")
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
