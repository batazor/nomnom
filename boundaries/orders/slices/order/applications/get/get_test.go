package get_test

import (
	"context"
	"errors"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/get"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var (
	customer = authz.Subject{Kind: authz.SubjectUser, ID: "customer-1"}
	stranger = authz.Subject{Kind: authz.SubjectUser, ID: "customer-2"}
	support  = authz.Subject{Kind: authz.SubjectService, ID: "support-console"}
)

const window = 5 * time.Minute

func setup(t *testing.T, seeded ...*order.Order) (get.Handler, *MockAuthorizer) {
	t.Helper()
	return setupWithPolicy(t, fixedWindow(t, window), seeded...)
}

func setupWithPolicy(t *testing.T, policy orderdetails.CancellationPolicy, seeded ...*order.Order) (get.Handler, *MockAuthorizer) {
	t.Helper()
	store := repositorytest.Open(t)
	authorizer := NewMockAuthorizer(t)
	for _, o := range seeded {
		require.NoError(t, store.Add(context.Background(), o))
	}
	return get.NewHandler(authorizer, store, policy), authorizer
}

func TestGetOrderRequiresASubject(t *testing.T) {
	handler, _ := setup(t, ordertest.New(t, "order-1"))

	_, err := handler.Handle(context.Background(), get.Query{Subject: authz.Subject{}, OrderID: "order-1"})

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}

func TestGetOrderFailsClosed(t *testing.T) {
	handler, authorizer := setup(t, ordertest.New(t, "order-1"))
	authorizer.EXPECT().
		Check(mock.Anything, authz.AccessCheck{
			Subject:    support,
			Permission: get.PermissionView,
			Resource:   authz.Resource{Type: get.ResourceOrder, ID: "order-1"},
		}).
		Return(errors.New("spicedb unavailable")).
		Once()

	_, err := handler.Handle(context.Background(), get.Query{Subject: support, OrderID: "order-1"})

	require.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// fixedWindow is the production cancellation policy with the given window.
func fixedWindow(t *testing.T, window time.Duration) cancellationpolicy.Fixed {
	t.Helper()
	policy, err := cancellationpolicy.NewFixed(window, time.Hour)
	require.NoError(t, err)
	return policy
}
