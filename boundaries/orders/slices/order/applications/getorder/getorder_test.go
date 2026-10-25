package getorder_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

var (
	customer = application.Subject{Kind: application.SubjectUser, ID: "customer-1"}
	stranger = application.Subject{Kind: application.SubjectUser, ID: "customer-2"}
	support  = application.Subject{Kind: application.SubjectService, ID: "support-console"}
)

func setup(t *testing.T, seeded ...*order.Order) (getorder.Handler, *memory.Authorizer) {
	t.Helper()
	store := memory.NewStore()
	authorizer := memory.NewAuthorizer()
	for _, o := range seeded {
		require.NoError(t, store.Add(context.Background(), o))
		require.NoError(t, authorizer.GrantOrderCustomer(context.Background(), o.ID(), o.CustomerID()))
	}
	return getorder.NewHandler(authorizer, store), authorizer
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
	assert.Equal(t, getorder.OrderView{
		OrderID:         "order-1",
		CustomerID:      "customer-1",
		RestaurantID:    "restaurant-1",
		Items:           []getorder.ItemView{{MenuItemID: "menu-1", Quantity: 2, UnitPrice: ordertest.Money(t, "10")}},
		DeliveryAddress: "Main street 1",
		Total:           ordertest.Money(t, "20"),
		State:           order.StateCancelled,
		PlacedAt:        ordertest.PlacedAt,
		Facts:           []getorder.FactView{{Fact: order.FactPickedUp, OccurredAt: pickedUp.OccurredAt}},
		Cancellation: &getorder.CancellationView{
			Outcome:     order.OutcomeCancellation,
			ReasonCode:  "customer_requested",
			CancelledAt: ordertest.PlacedAt,
		},
	}, view)
}

// AC-127: denial does not depend on existence; NOT_FOUND only for an authorized subject.
func TestGetOrderAuthorizesBeforeReading(t *testing.T) {
	handler, authorizer := setup(t, ordertest.New(t, "order-1"))
	authorizer.Allow(support, getorder.PermissionView, application.Resource{Type: getorder.ResourceOrder, ID: "missing"})

	tests := []struct {
		name    string
		subject application.Subject
		orderID order.ID
		wantErr error
	}{
		{name: "stranger and existing order", subject: stranger, orderID: "order-1", wantErr: application.ErrPermissionDenied},
		{name: "stranger and missing order", subject: stranger, orderID: "missing", wantErr: application.ErrPermissionDenied},
		{name: "authorized and missing order", subject: support, orderID: "missing", wantErr: order.ErrOrderNotFound},
		{name: "invalid subject", subject: application.Subject{}, orderID: "order-1", wantErr: application.ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := handler.Handle(context.Background(), getorder.Query{Subject: tt.subject, OrderID: tt.orderID})
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestGetOrderFailsClosed(t *testing.T) {
	handler, authorizer := setup(t, ordertest.New(t, "order-1"))
	authorizer.FailChecks(errors.New("spicedb unavailable"))

	_, err := handler.Handle(context.Background(), getorder.Query{Subject: customer, OrderID: "order-1"})

	require.ErrorIs(t, err, application.ErrPermissionDenied)
}
