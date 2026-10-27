package getorder_test

import (
	"context"
	"errors"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/mocks"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var (
	customer = application.Subject{Kind: application.SubjectUser, ID: "customer-1"}
	stranger = application.Subject{Kind: application.SubjectUser, ID: "customer-2"}
	support  = application.Subject{Kind: application.SubjectService, ID: "support-console"}
)

const window = 5 * time.Minute

func setup(t *testing.T, seeded ...*order.Order) (getorder.Handler, *mocks.MockAuthorizer) {
	t.Helper()
	return setupWithPolicy(t, fixedWindow(t, window), seeded...)
}

func setupWithPolicy(t *testing.T, policy orderdetails.CancellationPolicy, seeded ...*order.Order) (getorder.Handler, *mocks.MockAuthorizer) {
	t.Helper()
	store := repositorytest.Open(t)
	authorizer := mocks.NewMockAuthorizer(t)
	for _, o := range seeded {
		require.NoError(t, store.Add(context.Background(), o))
	}
	return getorder.NewHandler(authorizer, store, policy), authorizer
}

// AC-126: an authorized subject receives the full view.
func TestGetOrderReturnsView(t *testing.T) {
	pickedUp := order.RecordedFact{Fact: order.FactPickedUp, OccurredAt: ordertest.PlacedAt.Add(time.Hour)}
	handler, _ := setup(t, ordertest.New(t, "order-1",
		ordertest.WithState(order.StateCancelled),
		ordertest.WithFacts(pickedUp),
	))

	view, err := handler.Handle(context.Background(), getorder.Query{Subject: customer, OrderID: "order-1"})

	require.NoError(t, err)
	assert.Equal(t, orderdetails.View{Details: orderdetails.Details{
		OrderID:      "order-1",
		CustomerID:   "customer-1",
		MarketID:     ordertest.MarketID(t),
		RestaurantID: "restaurant-1",
		Items:        []orderdetails.Item{{MenuItemID: "menu-1", Name: "Margherita", Quantity: 2, UnitPrice: ordertest.Money(t, "10")}},
		Service:      order.StandardService(),
		Total:        ordertest.Money(t, "20"),
		State:        order.StateCancelled,
		PlacedAt:     ordertest.PlacedAt,
		Facts:        []orderdetails.Fact{{Fact: order.FactPickedUp, OccurredAt: pickedUp.OccurredAt}},
		Cancellation: &orderdetails.Cancellation{
			Outcome:     order.OutcomeCancellation,
			ReasonCode:  "customer_requested",
			CancelledAt: ordertest.PlacedAt,
		},
	}}, view)
}

// AC-127: denial does not depend on existence; NOT_FOUND only for an authorized subject.
func TestGetOrderAuthorizesBeforeReading(t *testing.T) {
	handler, authorizer := setup(t, ordertest.New(t, "order-1"))

	tests := []struct {
		name    string
		subject application.Subject
		orderID order.ID
		// checked is whether the view permission is checked; checkErr is its result.
		checked  bool
		checkErr error
		wantErr  error
	}{
		{name: "stranger and existing order", subject: stranger, orderID: "order-1", checked: true, checkErr: application.ErrPermissionDenied, wantErr: application.ErrPermissionDenied},
		{name: "stranger and missing order", subject: stranger, orderID: "missing", checked: true, checkErr: application.ErrPermissionDenied, wantErr: application.ErrPermissionDenied},
		{name: "authorized and missing order", subject: support, orderID: "missing", checked: true, wantErr: order.ErrOrderNotFound},
		{name: "invalid subject", subject: application.Subject{}, orderID: "order-1", wantErr: application.ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.checked {
				authorizer.EXPECT().
					Check(mock.Anything, application.AccessCheck{
						Subject:    tt.subject,
						Permission: getorder.PermissionView,
						Resource:   application.Resource{Type: getorder.ResourceOrder, ID: string(tt.orderID)},
					}).
					Return(tt.checkErr).
					Once()
			}
			_, err := handler.Handle(context.Background(), getorder.Query{Subject: tt.subject, OrderID: tt.orderID})
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// The Customer reads the own Order by customer_id, before orders-consumer
// synced order#customer (ADR 18): no permission check is made.
func TestGetOrderLetsTheCustomerReadTheOwnOrderWithoutSpiceDB(t *testing.T) {
	// The authorizer has no expectation: any permission check fails the test.
	handler, _ := setup(t, ordertest.New(t, "order-1"))

	view, err := handler.Handle(context.Background(), getorder.Query{Subject: customer, OrderID: "order-1"})

	require.NoError(t, err)
	assert.Equal(t, order.ID("order-1"), view.OrderID)
}

func TestGetOrderFailsClosed(t *testing.T) {
	handler, authorizer := setup(t, ordertest.New(t, "order-1"))
	authorizer.EXPECT().
		Check(mock.Anything, application.AccessCheck{
			Subject:    support,
			Permission: getorder.PermissionView,
			Resource:   application.Resource{Type: getorder.ResourceOrder, ID: "order-1"},
		}).
		Return(errors.New("spicedb unavailable")).
		Once()

	_, err := handler.Handle(context.Background(), getorder.Query{Subject: support, OrderID: "order-1"})

	require.ErrorIs(t, err, application.ErrPermissionDenied)
}

// AC-504: an Order that may still be cancelled carries the policy window.
func TestGetOrderPublishesCancellationWindowWhileCancellable(t *testing.T) {
	for _, state := range []order.State{order.StatePlaced, order.StateConfirmed} {
		t.Run(string(state), func(t *testing.T) {
			handler, _ := setup(t, ordertest.New(t, "order-1", ordertest.WithState(state)))

			view, err := handler.Handle(context.Background(), getorder.Query{Subject: customer, OrderID: "order-1"})

			require.NoError(t, err)
			assert.Equal(t, &order.CancellationWindow{
				OpensAt:  ordertest.PlacedAt,
				ClosesAt: ordertest.PlacedAt.Add(window),
			}, view.CancellationWindow)
		})
	}
}

// AC-505: a final Order has no window, whatever the policy says.
func TestGetOrderOmitsCancellationWindowForFinalOrders(t *testing.T) {
	for _, state := range []order.State{order.StateCancelled, order.StateCompleted} {
		t.Run(string(state), func(t *testing.T) {
			handler, _ := setupWithPolicy(t, fake.AlwaysOpen{}, ordertest.New(t, "order-1", ordertest.WithState(state)))

			view, err := handler.Handle(context.Background(), getorder.Query{Subject: customer, OrderID: "order-1"})

			require.NoError(t, err)
			assert.Nil(t, view.CancellationWindow)
		})
	}
}

// AC-507: a failing policy hides the window instead of failing the read.
func TestGetOrderHidesCancellationWindowWhenPolicyFails(t *testing.T) {
	policy := NewMockCancellationPolicy(t)
	policy.EXPECT().
		Window(mock.Anything, ordertest.PlacedAt).
		Return(order.CancellationWindow{}, errors.New("policy unavailable")).
		Once()
	handler, _ := setupWithPolicy(t, policy, ordertest.New(t, "order-1"))

	view, err := handler.Handle(context.Background(), getorder.Query{Subject: customer, OrderID: "order-1"})

	require.NoError(t, err)
	assert.Equal(t, order.ID("order-1"), view.OrderID)
	assert.Nil(t, view.CancellationWindow)
}

// fixedWindow is the production cancellation policy with the given window.
func fixedWindow(t *testing.T, window time.Duration) cancellationpolicy.Fixed {
	t.Helper()
	policy, err := cancellationpolicy.NewFixed(window)
	require.NoError(t, err)
	return policy
}
