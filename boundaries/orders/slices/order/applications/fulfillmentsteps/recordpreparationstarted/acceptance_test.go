package recordpreparationstarted_test

import (
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordpreparationstarted"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func input(eventID string) recordpreparationstarted.Input {
	return recordpreparationstarted.Input{EventID: eventID, OrderID: "order-1", OccurredAt: occurredAt}
}

// TestAcceptance runs acceptance/recordpreparationstarted.feature (ADR 19). A scenario is a
// subtest; its mocks report to it through godog.T. The store and the cleanup of
// the mocks belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	recorded := order.AppliedTransition{Trigger: order.TriggerStartPreparation, OccurredAt: occurredAt}
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				store    *repositorytest.Store
				workflow *MockNotifier
				handler  recordpreparationstarted.Handler
				state    order.State
				result   recordpreparationstarted.Result
			)
			given := func(ctx context.Context, options ...ordertest.Option) {
				store = repositorytest.Open(t)
				workflow = NewMockNotifier(t)
				workflow.Test(godog.T(ctx))
				require.NoError(godog.T(ctx), store.Add(ctx, ordertest.New(t, "order-1", options...)))
				handler = recordpreparationstarted.NewHandler(store, store, store, workflow)
			}

			sc.Given(`^an order is in fulfillment$`, func(ctx context.Context) {
				given(ctx)
			})
			sc.Given(`^the order has recorded start of preparation from a report$`, func(ctx context.Context) {
				given(ctx)
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationStarted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				_, err := handler.Handle(ctx, input("event-1"))
				require.NoError(godog.T(ctx), err)
			})
			sc.Given(`^an order has already recorded start of preparation$`, func(ctx context.Context) {
				given(ctx, ordertest.WithTransitions(recorded))
			})
			sc.Given(`^an order is (completed|cancelled)$`, func(ctx context.Context, final string) {
				state = order.State(final)
				given(ctx, ordertest.WithState(state))
			})

			sc.When(`^the restaurant starts preparing the order$`, func(ctx context.Context) {
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationStarted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				var err error
				result, err = handler.Handle(ctx, input("event-1"))
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the same report is delivered again$`, func(ctx context.Context) {
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationStarted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				var err error
				result, err = handler.Handle(ctx, input("event-1"))
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^a new report states the same fact$`, func(ctx context.Context) {
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-2", Kind: fulfillment.KindPreparationStarted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				var err error
				result, err = handler.Handle(ctx, input("event-2"))
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^a report arrives after order fulfillment is closed$`, func(ctx context.Context) {
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationStarted, OccurredAt: occurredAt,
					}).
					Return(fulfillment.ErrWorkflowNotFound).
					Once()
				var err error
				result, err = handler.Handle(ctx, input("event-1"))
				require.NoError(godog.T(ctx), err)
			})

			sc.Then(`^the order records start of preparation$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeApplied, result.Outcome)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StatePreparing, snapshot.State)
				assert.Equal(godog.T(ctx), []order.AppliedTransition{recorded}, snapshot.Transitions)
				assert.Equal(godog.T(ctx), 1, store.InboxSize())
			})
			sc.Then(`^order fulfillment is told of the fact$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Notify", 1)
			})
			sc.Then(`^the order does not change again$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeDuplicate, result.Outcome)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), uint64(2), snapshot.Version, "the duplicate must not save the Order")
				assert.Equal(godog.T(ctx), 1, store.InboxSize())
			})
			sc.Then(`^order fulfillment is told of the report again$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Notify", 2)
			})
			sc.Then(`^the order still records start of preparation once$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeAlreadyApplied, result.Outcome)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StatePlaced, snapshot.State)
				assert.Equal(godog.T(ctx), []order.AppliedTransition{recorded}, snapshot.Transitions)
				assert.Equal(godog.T(ctx), uint64(1), snapshot.Version)
			})
			sc.Then(`^order fulfillment is told of the new report$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Notify", 1)
			})
			sc.Then(`^the order does not change$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), state, snapshot.State)
				assert.Empty(godog.T(ctx), snapshot.Transitions)
				assert.Equal(godog.T(ctx), uint64(1), snapshot.Version)
			})
			sc.Then(`^the report counts as processed late$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), recordpreparationstarted.Result{Outcome: fulfillment.OutcomeIgnoredLate}, result)
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
