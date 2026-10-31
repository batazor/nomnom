package requestordercancellation_test

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
	"nomnom/orders/slices/order/applications/closure/requestordercancellation"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var customer = authz.Subject{Kind: authz.SubjectUser, ID: "customer-1"}

type env struct {
	store      *repositorytest.Store
	authorizer *MockAuthorizer
	workflow   *MockCancellationRequester
	clock      *fake.Clock
	handler    requestordercancellation.Handler
}

func newEnv(t *testing.T, state order.State, policy requestordercancellation.CancellationPolicy) env {
	t.Helper()
	e := env{
		store:      repositorytest.Open(t),
		authorizer: NewMockAuthorizer(t),
		workflow:   NewMockCancellationRequester(t),
		clock:      fake.NewClock(ordertest.PlacedAt.Add(5 * time.Minute)),
	}
	ctx := context.Background()
	require.NoError(t, e.store.Add(ctx, ordertest.New(t, "order-1", ordertest.WithState(state))))
	e.handler = requestordercancellation.NewHandler(requestordercancellation.Dependencies{
		Authorizer: e.authorizer,
		Orders:     e.store,
		Policy:     policy,
		Clock:      e.clock,
		Workflow:   e.workflow,
	})
	return e
}

func input(subject authz.Subject) requestordercancellation.Input {
	return requestordercancellation.Input{Subject: subject, OrderID: "order-1", RequestID: "cancel-1", Reason: "changed_mind"}
}

// AC-115: a denied subject learns nothing about a missing Order either.
func TestRequestCancellationDeniesAMissingOrder(t *testing.T) {
	stranger := authz.Subject{Kind: authz.SubjectUser, ID: "customer-2"}
	e := newEnv(t, order.StatePlaced, fake.AlwaysOpen{})
	e.authorizer.EXPECT().
		Check(mock.Anything, authz.AccessCheck{
			Subject:    stranger,
			Permission: requestordercancellation.PermissionCancel,
			Resource:   authz.Resource{Type: requestordercancellation.ResourceOrder, ID: "missing"},
		}).
		Return(authz.ErrPermissionDenied).
		Once()
	missing := input(stranger)
	missing.OrderID = "missing"

	_, err := e.handler.Handle(context.Background(), missing)

	require.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestRequestCancellationReportsWorkflowRefusalAndInvalidInput(t *testing.T) {
	e := newEnv(t, order.StatePlaced, fake.AlwaysOpen{})
	refusal := errors.New("already picked up")
	e.workflow.EXPECT().
		RequestCancellation(mock.Anything, fulfillment.CancellationRequest{
			OrderID:     "order-1",
			RequestID:   "cancel-1",
			RequestedBy: customer,
			RequestedAt: e.clock.Now(),
			Reason:      "changed_mind",
		}).
		Return(refusal).
		Once()

	_, err := e.handler.Handle(context.Background(), input(customer))
	require.ErrorIs(t, err, refusal)

	noRequestID := input(customer)
	noRequestID.RequestID = ""
	_, err = e.handler.Handle(context.Background(), noRequestID)
	// The authorizer has no expectation: the Customer is not checked; invalid
	// input is rejected before any read.
	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}

// fixedWindow is the production cancellation policy with the given window.
func fixedWindow(t *testing.T, window time.Duration) cancellationpolicy.Fixed {
	t.Helper()
	policy, err := cancellationpolicy.NewFixed(window, 60*time.Minute)
	require.NoError(t, err)
	return policy
}
