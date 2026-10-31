package recordpreparationdelay_test

import (
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/confirm"
	"nomnom/orders/slices/order/applications/recordpreparationdelay"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestAcceptance runs acceptance/recordpreparationdelay.feature (ADR 19). A scenario is a subtest;
// its mocks report to it through godog.T. The store and the cleanup of the mocks
// belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	readyAt := occurredAt.Add(12 * time.Minute)
	delayedTo := occurredAt.Add(20 * time.Minute)
	reportedAt := occurredAt.Add(5 * time.Minute)
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				store    *repositorytest.Store
				workflow *MockNotifier
				handler  recordpreparationdelay.Handler
				result   recordpreparationdelay.Result
			)
			given := func(ctx context.Context, options ...ordertest.Option) {
				store = repositorytest.Open(t)
				workflow = NewMockNotifier(t)
				workflow.Test(godog.T(ctx))
				require.NoError(godog.T(ctx), store.Add(ctx, ordertest.New(t, "order-1", options...)))
				handler = recordpreparationdelay.NewHandler(store, store, store, workflow)
			}

			sc.Given(`^the restaurant has accepted an order and named a ready time$`, func(ctx context.Context) {
				given(ctx)
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "accepted", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
					}).
					Return(nil).
					Once()
				_, err := confirm.NewHandler(store, store, store, workflow).Handle(ctx, confirm.Input{
					EventID: "accepted", OrderID: "order-1", OccurredAt: occurredAt, ExpectedReadyAt: readyAt,
				})
				require.NoError(godog.T(ctx), err)
			})
			sc.Given(`^an order has recorded a delay$`, func(ctx context.Context) {
				given(ctx)
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "delayed", Kind: fulfillment.KindPreparationDelayed, OccurredAt: reportedAt, ExpectedReadyAt: delayedTo,
					}).
					Return(nil).
					Once()
				_, err := handler.Handle(ctx, delay("delayed", delayedTo))
				require.NoError(godog.T(ctx), err)
			})
			sc.Given(`^an order is (completed|cancelled)$`, func(ctx context.Context, state string) {
				given(ctx, ordertest.WithState(order.State(state)))
			})

			sc.When(`^the restaurant reports a delay with a later ready time$`, func(ctx context.Context) {
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "delayed", Kind: fulfillment.KindPreparationDelayed, OccurredAt: reportedAt, ExpectedReadyAt: delayedTo,
					}).
					Return(nil).
					Once()
				var err error
				result, err = handler.Handle(ctx, delay("delayed", delayedTo))
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the same delay is delivered again$`, func(ctx context.Context) {
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "delayed", Kind: fulfillment.KindPreparationDelayed, OccurredAt: reportedAt, ExpectedReadyAt: delayedTo,
					}).
					Return(nil).
					Once()
				var err error
				result, err = handler.Handle(ctx, delay("delayed", delayedTo))
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^a delay arrives after order fulfillment is closed$`, func(ctx context.Context) {
				workflow.EXPECT().
					Notify(mock.Anything, fulfillment.Notification{
						OrderID: "order-1", EventID: "delayed", Kind: fulfillment.KindPreparationDelayed, OccurredAt: reportedAt, ExpectedReadyAt: delayedTo,
					}).
					Return(fulfillment.ErrWorkflowNotFound).
					Once()
				var err error
				result, err = handler.Handle(ctx, delay("delayed", delayedTo))
				require.NoError(godog.T(ctx), err)
			})

			sc.Then(`^the order expects the later ready time$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeApplied, result.Outcome)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.ExpectedTimes{ReadyAt: delayedTo}, snapshot.Expected)
			})
			sc.Then(`^the order stays confirmed$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StateConfirmed, snapshot.State)
			})
			sc.Then(`^order fulfillment is told of the later ready time$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
			})
			sc.Then(`^the order does not change again$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), fulfillment.OutcomeDuplicate, result.Outcome)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), uint64(2), snapshot.Version, "the duplicate must not save the Order")
				assert.Equal(godog.T(ctx), 1, store.InboxSize())
			})
			sc.Then(`^order fulfillment is told of the delay again$`, func(ctx context.Context) {
				assert.True(godog.T(ctx), result.WorkflowNotified)
				workflow.AssertNumberOfCalls(godog.T(ctx), "Notify", 2)
			})
			sc.Then(`^the order expects no ready time$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
				assert.Zero(godog.T(ctx), snapshot.Expected)
				assert.Equal(godog.T(ctx), uint64(1), snapshot.Version)
			})
			sc.Then(`^the delay counts as processed late$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), recordpreparationdelay.Result{Outcome: fulfillment.OutcomeIgnoredLate}, result)
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
