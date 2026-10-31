package recordpaymentauthorization_test

import (
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordpaymentauthorization"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestAcceptance runs acceptance/recordpaymentauthorization.feature (ADR 19). The
// store belongs to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				handler recordpaymentauthorization.Handler
				store   *repositorytest.Store
				result  recordpaymentauthorization.Result
				failure error
			)
			once := []order.AppliedTransition{{Trigger: order.TriggerAuthorizePayment, OccurredAt: authorizedAt}}

			sc.Given(`^an order is (placed|confirmed|cancelled|completed)$`, func(state string) {
				handler, store = setup(t, order.State(state))
			})
			sc.Given(`^an order has recorded the payment authorization$`, func(ctx context.Context) {
				handler, store = setup(t, order.StatePlaced)
				_, err := handler.Handle(ctx, recordpaymentauthorization.Input{OrderID: "order-1", AuthorizedAt: authorizedAt})
				require.NoError(godog.T(ctx), err)
			})

			sc.When(`^order fulfillment records the payment authorization$`, func(ctx context.Context) {
				var err error
				result, err = handler.Handle(ctx, recordpaymentauthorization.Input{OrderID: "order-1", AuthorizedAt: authorizedAt})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^order fulfillment records the payment authorization again$`, func(ctx context.Context) {
				var err error
				result, err = handler.Handle(ctx, recordpaymentauthorization.Input{OrderID: "order-1", AuthorizedAt: authorizedAt.Add(time.Minute)})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^order fulfillment tries to record the payment authorization$`, func(ctx context.Context) {
				_, failure = handler.Handle(ctx, recordpaymentauthorization.Input{OrderID: "order-1", AuthorizedAt: authorizedAt})
			})

			sc.Then(`^the order records the payment authorization at its moment$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), recordpaymentauthorization.Result{Outcome: recordpaymentauthorization.OutcomeRecorded}, result)
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.StatePaid, snapshot.State)
				assert.Equal(godog.T(ctx), once, snapshot.Transitions)
			})
			sc.Then(`^neighbouring contexts are not told of it$`, func(ctx context.Context) {
				assert.Empty(godog.T(ctx), store.OutboxRecords())
			})
			sc.Then(`^the order reports it as already recorded$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), recordpaymentauthorization.Result{Outcome: recordpaymentauthorization.OutcomeAlreadyRecorded}, result)
			})
			sc.Then(`^the order still records one payment authorization$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), uint64(2), snapshot.Version)
				assert.Equal(godog.T(ctx), once, snapshot.Transitions)
			})
			sc.Then(`^the payment authorization is refused$`, func(ctx context.Context) {
				var invalid order.InvalidTransition
				assert.ErrorAs(godog.T(ctx), failure, &invalid)
			})
			sc.Then(`^the order records no payment authorization$`, func(ctx context.Context) {
				snapshot, _ := store.Snapshot("order-1")
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
