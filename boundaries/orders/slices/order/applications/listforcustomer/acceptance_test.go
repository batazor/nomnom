package listforcustomer_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/slices/order/applications/listforcustomer"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

// TestAcceptance runs acceptance/listforcustomer.feature (ADR 19). A scenario
// is a subtest; its mocks report to it through godog.T. The store and the cleanup
// of the mocks belong to t: godog.T has no Cleanup.
func TestAcceptance(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var (
				handler    listforcustomer.Handler
				authorizer *MockAuthorizer
				first      listforcustomer.Page
				second     listforcustomer.Page
				failure    error
			)

			sc.Given(`^a customer has three orders and another customer has one$`, func(ctx context.Context) {
				handler, authorizer = setup(t)
				authorizer.Test(godog.T(ctx))
			})
			sc.Given(`^the customer has an order in hamburg$`, func() {
				// setup places order-h in hamburg, between the orders of berlin by placed_at.
			})

			sc.When(`^the customer reads the history two orders at a time$`, func(ctx context.Context) {
				// The authorizer has no expectation: the own history needs no permission check.
				var err error
				first, err = handler.Handle(ctx, listforcustomer.Query{Subject: customer, CustomerID: "customer-1", MarketID: ordertest.Market, Limit: 2})
				require.NoError(godog.T(ctx), err)
				second, err = handler.Handle(ctx, listforcustomer.Query{
					Subject: customer, CustomerID: "customer-1", MarketID: ordertest.Market, Limit: 2, Cursor: first.NextCursor,
				})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^another customer without the permission reads the history of the customer$`, func(ctx context.Context) {
				authorizer.EXPECT().
					Check(mock.Anything, authz.AccessCheck{
						Subject:    stranger,
						Permission: listforcustomer.PermissionViewOrders,
						Resource:   authz.Resource{Type: listforcustomer.ResourceCustomer, ID: "customer-1"},
					}).
					Return(authz.ErrPermissionDenied).
					Once()
				_, failure = handler.Handle(ctx, listforcustomer.Query{Subject: stranger, CustomerID: "customer-1", MarketID: ordertest.Market})
			})
			sc.When(`^a support subject with the permission reads the history of the customer$`, func(ctx context.Context) {
				authorizer.EXPECT().
					Check(mock.Anything, authz.AccessCheck{
						Subject:    support,
						Permission: listforcustomer.PermissionViewOrders,
						Resource:   authz.Resource{Type: listforcustomer.ResourceCustomer, ID: "customer-1"},
					}).
					Return(nil).
					Once()
				var err error
				first, err = handler.Handle(ctx, listforcustomer.Query{Subject: support, CustomerID: "customer-1", MarketID: ordertest.Market})
				require.NoError(godog.T(ctx), err)
			})
			sc.When(`^the customer reads the history in ([a-z]+)$`, func(ctx context.Context, market string) {
				var err error
				first, err = handler.Handle(ctx, listforcustomer.Query{Subject: customer, CustomerID: "customer-1", MarketID: market})
				require.NoError(godog.T(ctx), err)
			})

			sc.Then(`^the first page holds the two newest orders of the customer$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), []order.ID{"order-c", "order-b"}, ids(first))
			})
			sc.Then(`^the next page holds the oldest order and ends the history$`, func(ctx context.Context) {
				assert.Equal(godog.T(ctx), []order.ID{"order-a"}, ids(second))
				assert.Empty(godog.T(ctx), second.NextCursor)
			})
			sc.Then(`^every order of the history carries its full card$`, func(ctx context.Context) {
				require.Len(godog.T(ctx), second.Orders, 1)
				last := second.Orders[0]
				assert.NotEmpty(godog.T(ctx), last.Cursor, "every order carries the cursor that continues after it")
				last.Cursor = ""
				assert.Equal(godog.T(ctx), listforcustomer.Entry{View: orderdetails.View{
					Details: orderdetails.Details{
						OrderID:      "order-a",
						CustomerID:   "customer-1",
						MarketID:     ordertest.MarketID(t),
						RestaurantID: "restaurant-1",
						Items:        []orderdetails.Item{{MenuItemID: "menu-1", Name: "Margherita", Quantity: 2, UnitPrice: ordertest.Money(t, "10")}},
						Service:      order.StandardService(),
						Total:        ordertest.Money(t, "20"),
						State:        order.StatePlaced,
						PlacedAt:     ordertest.PlacedAt,
						Transitions:  []order.AppliedTransition{},
					},
					CancellationWindow: &order.CancellationWindow{OpensAt: ordertest.PlacedAt, ClosesAt: ordertest.PlacedAt.Add(window)},
				}}, last, "the history carries the full card with its Cancellation Window")
			})
			sc.Then(`^access is denied$`, func(ctx context.Context) {
				assert.ErrorIs(godog.T(ctx), failure, authz.ErrPermissionDenied)
			})
			sc.Then(`^the history lists (.+)$`, func(ctx context.Context, listed string) {
				want := []order.ID{}
				if listed != "no orders" {
					for id := range strings.SplitSeq(listed, ", ") {
						want = append(want, order.ID(id))
					}
				}
				assert.Equal(godog.T(ctx), want, ids(first))
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
