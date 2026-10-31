package getorder_test

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
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
)

// TestAcceptance runs acceptance/getorder.feature (ADR 19). A scenario is a
// subtest; its mocks report to it through godog.T. The store and the cleanup of
// the mocks belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	pickedUp := order.AppliedTransition{Trigger: order.TriggerPickUp, OccurredAt: ordertest.PlacedAt.Add(time.Hour)}
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				handler    getorder.Handler
				authorizer *MockAuthorizer
				view       orderdetails.View
				failure    error
			)

			sc.Given(`^a customer's order was cancelled after its pickup$`, func(ctx context.Context) {
				handler, authorizer = setup(t, ordertest.New(t, "order-1",
					ordertest.WithState(order.StateCancelled),
					ordertest.WithTransitions(pickedUp),
				))
				authorizer.Test(godog.T(ctx))
			})
			sc.Given(`^an order is (placed|confirmed)$`, func(ctx context.Context, state string) {
				handler, authorizer = setup(t, ordertest.New(t, "order-1", ordertest.WithState(order.State(state))))
				authorizer.Test(godog.T(ctx))
			})
			sc.Given(`^an order is (cancelled|completed)$`, func(ctx context.Context, state string) {
				// A final Order has no window, whatever the policy says.
				handler, authorizer = setupWithPolicy(t, fake.AlwaysOpen{}, ordertest.New(t, "order-1", ordertest.WithState(order.State(state))))
				authorizer.Test(godog.T(ctx))
			})
			sc.Given(`^an order is placed and the cancellation policy is failing$`, func(ctx context.Context) {
				policy := NewMockCancellationPolicy(t)
				policy.Test(godog.T(ctx))
				policy.EXPECT().
					Window(mock.Anything, ordertest.PlacedAt, order.DeliveryWindow{}).
					Return(order.CancellationWindow{}, errors.New("policy unavailable")).
					Once()
				handler, authorizer = setupWithPolicy(t, policy, ordertest.New(t, "order-1"))
				authorizer.Test(godog.T(ctx))
			})

			sc.When(`^the customer reads the order$`, func(ctx context.Context) {
				// The authorizer has no expectation: the Customer reads the own Order by
				// customer_id, before orders-consumer synced order#customer (ADR 18).
				var err error
				view, err = handler.Handle(ctx, getorder.Query{Subject: customer, OrderID: "order-1"})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^another customer without the permission reads (an existing|a missing) order$`, func(ctx context.Context, target string) {
				orderID := order.ID("order-1")
				if target == "a missing" {
					orderID = "missing"
				}
				authorizer.EXPECT().
					Check(mock.Anything, authz.AccessCheck{
						Subject:    stranger,
						Permission: getorder.PermissionView,
						Resource:   authz.Resource{Type: getorder.ResourceOrder, ID: string(orderID)},
					}).
					Return(authz.ErrPermissionDenied).
					Once()
				_, failure = handler.Handle(ctx, getorder.Query{Subject: stranger, OrderID: orderID})
			})
			sc.When(`^a support subject with the permission reads a missing order$`, func(ctx context.Context) {
				authorizer.EXPECT().
					Check(mock.Anything, authz.AccessCheck{
						Subject:    support,
						Permission: getorder.PermissionView,
						Resource:   authz.Resource{Type: getorder.ResourceOrder, ID: "missing"},
					}).
					Return(nil).
					Once()
				_, failure = handler.Handle(ctx, getorder.Query{Subject: support, OrderID: "missing"})
			})

			sc.Then(`^the customer sees its items, total, state, recorded facts and cancellation reason$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), orderdetails.View{Details: orderdetails.Details{
					OrderID:      "order-1",
					CustomerID:   "customer-1",
					MarketID:     ordertest.MarketID(t),
					RestaurantID: "restaurant-1",
					Items:        []orderdetails.Item{{MenuItemID: "menu-1", Name: "Margherita", Quantity: 2, UnitPrice: ordertest.Money(t, "10")}},
					Service:      order.StandardService(),
					Total:        ordertest.Money(t, "20"),
					State:        order.StateCancelled,
					PlacedAt:     ordertest.PlacedAt,
					Transitions:  []order.AppliedTransition{{Trigger: order.TriggerPickUp, OccurredAt: pickedUp.OccurredAt}},
					Cancellation: &orderdetails.Cancellation{
						Outcome:     order.OutcomeCancellation,
						ReasonCode:  "customer_requested",
						CancelledAt: ordertest.PlacedAt,
					},
				}}, view)
			})
			sc.Then(`^access is denied$`, func(ctx context.Context) {
				assert.ErrorIs(godog.T(ctx), failure, authz.ErrPermissionDenied)
			})
			sc.Then(`^the order is reported as not found$`, func(ctx context.Context) {
				assert.ErrorIs(godog.T(ctx), failure, order.ErrOrderNotFound)
			})
			sc.Then(`^the order shows the cancellation window from its placement$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), &order.CancellationWindow{
					OpensAt:  ordertest.PlacedAt,
					ClosesAt: ordertest.PlacedAt.Add(window),
				}, view.CancellationWindow)
			})
			sc.Then(`^the order shows no cancellation window$`, func(ctx context.Context) {
				assert.Nil(godog.T(ctx), view.CancellationWindow)
			})
			sc.Then(`^the order is shown$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), order.ID("order-1"), view.OrderID)
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
