package requestcancellation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/slices/order/applications/closure/requestcancellation"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
)

// TestAcceptance runs acceptance/requestcancellation.feature (ADR 19). A
// scenario is a subtest; its mocks report to it through godog.T. The store and the
// cleanup of the mocks belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	windowStart := time.Date(2026, time.October, 25, 19, 0, 0, 0, time.UTC)
	stranger := authz.Subject{Kind: authz.SubjectUser, ID: "customer-2"}
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				e       env
				result  requestcancellation.Result
				failure error
			)
			given := func(ctx context.Context, state order.State, policy requestcancellation.CancellationPolicy) {
				e = newEnv(t, state, policy)
				e.authorizer.Test(godog.T(ctx))
				e.workflow.Test(godog.T(ctx))
			}
			// scheduled is the handler of a Scheduled Order at the moment of the request.
			scheduled := func(requestedAt time.Time) requestcancellation.Handler {
				return requestcancellation.NewHandler(requestcancellation.Dependencies{
					Authorizer: e.authorizer,
					Orders:     e.store,
					Policy:     fixedWindow(t, 5*time.Minute),
					Clock:      fake.NewClock(requestedAt),
					Workflow:   e.workflow,
				})
			}

			sc.Given(`^an order is (placed|confirmed|completed|cancelled)$`, func(ctx context.Context, state string) {
				given(ctx, order.State(state), fake.AlwaysOpen{})
			})
			sc.Given(`^an order is confirmed and its cancellation window has expired$`, func(ctx context.Context) {
				given(ctx, order.StateConfirmed, fixedWindow(t, time.Minute))
			})
			sc.Given(`^a scheduled order with a delivery window at 19:00 and a cutoff of 60 minutes$`, func(ctx context.Context) {
				given(ctx, order.StatePlaced, fake.AlwaysOpen{})
				require.NoError(godog.T(ctx), e.store.Add(ctx, ordertest.New(t, "order-scheduled",
					ordertest.WithDeliveryWindow(t, windowStart, windowStart.Add(15*time.Minute)))))
			})

			sc.When(`^the customer requests its cancellation$`, func(ctx context.Context) {
				e.workflow.EXPECT().
					RequestCancellation(mock.Anything, fulfillment.CancellationRequest{
						OrderID:     "order-1",
						RequestID:   "cancel-1",
						RequestedBy: customer,
						RequestedAt: e.clock.Now(),
						Reason:      "changed_mind",
					}).
					Return(nil).
					Once()
				var err error
				result, err = e.handler.Handle(ctx, input(customer))
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the customer requests its cancellation at 17:30$`, func(ctx context.Context) {
				requestedAt := time.Date(2026, time.October, 25, 17, 30, 0, 0, time.UTC)
				e.workflow.EXPECT().
					RequestCancellation(mock.Anything, fulfillment.CancellationRequest{
						OrderID: "order-scheduled", RequestID: "cancel-1", RequestedBy: customer, RequestedAt: requestedAt, Reason: "changed_mind",
					}).
					Return(nil).
					Once()
				in := input(customer)
				in.OrderID = "order-scheduled"
				var err error
				result, err = scheduled(requestedAt).Handle(ctx, in)
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the customer tries to cancel it at 18:10$`, func(ctx context.Context) {
				in := input(customer)
				in.OrderID = "order-scheduled"
				result, failure = scheduled(time.Date(2026, time.October, 25, 18, 10, 0, 0, time.UTC)).Handle(ctx, in)
			})
			sc.When(`^another customer requests its cancellation without the permission$`, func(ctx context.Context) {
				e.authorizer.EXPECT().
					Check(mock.Anything, authz.AccessCheck{
						Subject:    stranger,
						Permission: requestcancellation.PermissionCancel,
						Resource:   authz.Resource{Type: requestcancellation.ResourceOrder, ID: "order-1"},
					}).
					Return(authz.ErrPermissionDenied).
					Once()
				_, failure = e.handler.Handle(ctx, input(stranger))
			})
			sc.When(`^another customer requests its cancellation while authorization is failing$`, func(ctx context.Context) {
				e.authorizer.EXPECT().
					Check(mock.Anything, authz.AccessCheck{
						Subject:    stranger,
						Permission: requestcancellation.PermissionCancel,
						Resource:   authz.Resource{Type: requestcancellation.ResourceOrder, ID: "order-1"},
					}).
					Return(errors.New("spicedb timeout")).
					Once()
				_, failure = e.handler.Handle(ctx, input(stranger))
			})
			sc.When(`^the customer tries to cancel it$`, func(ctx context.Context) {
				result, failure = e.handler.Handle(ctx, input(customer))
			})

			sc.Then(`^order fulfillment receives the cancellation request$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), requestcancellation.Result{Outcome: requestcancellation.OutcomeRequested}, result)
				// The Customer cancels the own Order by customer_id, before
				// orders-consumer synced order#customer (ADR 18).
				e.authorizer.AssertNotCalled(godog.T(ctx), "Check", mock.Anything, mock.Anything)
			})
			sc.Then(`^the order stays (placed|confirmed)$`, func(ctx context.Context, state string) {
				snapshot, _ := e.store.Snapshot("order-1")
				assert.Equal(godog.T(ctx), order.State(state), snapshot.State)
				assert.Empty(godog.T(ctx), e.store.OutboxRecords())
			})
			sc.Then(`^the request is denied$`, func(ctx context.Context) {
				assert.ErrorIs(godog.T(ctx), failure, authz.ErrPermissionDenied)
			})
			sc.Then(`^order fulfillment receives no request$`, func(ctx context.Context) {
				e.workflow.AssertNotCalled(godog.T(ctx), "RequestCancellation", mock.Anything, mock.Anything)
			})
			sc.Then(`^the request is refused because of the order state$`, func(ctx context.Context) {
				var invalid order.InvalidTransition
				assert.ErrorAs(godog.T(ctx), failure, &invalid)
			})
			sc.Then(`^the request is refused because the cancellation window has expired$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), order.CancellationNotAllowed{
					Reasons: []order.PolicyReason{{Code: order.CancellationWindowExpired}},
				}, failure)
			})
			sc.Then(`^the order is reported as already cancelled$`, func(ctx context.Context) {
				require.NoError(godog.T(ctx), failure)
				assert.Equal(godog.T(ctx), requestcancellation.OutcomeAlreadyCancelled, result.Outcome)
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
