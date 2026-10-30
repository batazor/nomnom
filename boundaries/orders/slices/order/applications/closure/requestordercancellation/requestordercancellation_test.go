package requestordercancellation_test

import (
	"context"
	"errors"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

// AC-116: an admissible request reaches the workflow and leaves the Order unchanged.
func TestRequestCancellationIsHandedToWorkflow(t *testing.T) {
	for _, state := range []order.State{order.StatePlaced, order.StateConfirmed} {
		t.Run(string(state), func(t *testing.T) {
			e := newEnv(t, state, fake.AlwaysOpen{})
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

			result, err := e.handler.Handle(context.Background(), input(customer))

			require.NoError(t, err)
			assert.Equal(t, requestordercancellation.Result{Outcome: requestordercancellation.OutcomeRequested}, result)
			snapshot, _ := e.store.Snapshot("order-1")
			assert.Equal(t, state, snapshot.State)
			assert.Empty(t, e.store.OutboxRecords())
		})
	}
}

// AC-115: authorization is checked first and fails closed.
func TestRequestCancellationRequiresPermission(t *testing.T) {
	stranger := authz.Subject{Kind: authz.SubjectUser, ID: "customer-2"}
	tests := []struct {
		name     string
		subject  authz.Subject
		checkErr error
	}{
		{name: "another customer", subject: stranger, checkErr: authz.ErrPermissionDenied},
		{name: "authorization unavailable", subject: stranger, checkErr: errors.New("spicedb timeout")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The workflow has no expectation: a cancellation request fails the test.
			e := newEnv(t, order.StatePlaced, fake.AlwaysOpen{})
			e.authorizer.EXPECT().
				Check(mock.Anything, authz.AccessCheck{
					Subject:    tt.subject,
					Permission: requestordercancellation.PermissionCancel,
					Resource:   authz.Resource{Type: requestordercancellation.ResourceOrder, ID: "order-1"},
				}).
				Return(tt.checkErr).
				Once()

			_, err := e.handler.Handle(context.Background(), input(tt.subject))

			require.ErrorIs(t, err, authz.ErrPermissionDenied)
		})
	}

	// A denied subject learns nothing about a missing Order either.
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

// The Customer cancels the own Order by customer_id, before orders-consumer
// synced order#customer (ADR 18): no permission check is made.
func TestRequestCancellationLetsTheCustomerCancelWithoutSpiceDB(t *testing.T) {
	// The authorizer has no expectation: any permission check fails the test.
	e := newEnv(t, order.StatePlaced, fake.AlwaysOpen{})
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

	result, err := e.handler.Handle(context.Background(), input(customer))

	require.NoError(t, err)
	assert.Equal(t, requestordercancellation.OutcomeRequested, result.Outcome)
}

// AC-117, AC-118, AC-119: the Order and the cancellation window decide admissibility.
func TestRequestCancellationRespectsDomainDecision(t *testing.T) {
	tests := []struct {
		name    string
		state   order.State
		policy  requestordercancellation.CancellationPolicy
		outcome requestordercancellation.Outcome
		check   func(t *testing.T, err error)
	}{
		{
			name:   "completed order",
			state:  order.StateCompleted,
			policy: fake.AlwaysOpen{},
			check: func(t *testing.T, err error) {
				var invalid order.InvalidTransition
				require.ErrorAs(t, err, &invalid)
			},
		},
		{
			name:   "window expired",
			state:  order.StateConfirmed,
			policy: fixedWindow(t, time.Minute),
			check: func(t *testing.T, err error) {
				assert.Equal(t, order.CancellationNotAllowed{
					Reasons: []order.PolicyReason{{Code: order.CancellationWindowExpired}},
				}, err)
			},
		},
		{
			name:    "already cancelled",
			state:   order.StateCancelled,
			policy:  fake.AlwaysOpen{},
			outcome: requestordercancellation.OutcomeAlreadyCancelled,
			check:   func(t *testing.T, err error) { require.NoError(t, err) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The workflow has no expectation: a cancellation request fails the test.
			e := newEnv(t, tt.state, tt.policy)

			result, err := e.handler.Handle(context.Background(), input(customer))

			tt.check(t, err)
			assert.Equal(t, tt.outcome, result.Outcome)
		})
	}
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

// AC-916: window 19:00, CANCELLATION__SCHEDULED_CUTOFF 60 minutes: a request
// at 17:30 reaches the workflow, one at 18:10 is refused as an expired window.
func TestRequestCancellationOfScheduledOrderClosesBeforeItsWindow(t *testing.T) {
	windowStart := time.Date(2026, time.October, 25, 19, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		requestedAt time.Time
		expired     bool
	}{
		"17:30": {requestedAt: time.Date(2026, time.October, 25, 17, 30, 0, 0, time.UTC)},
		"18:10": {requestedAt: time.Date(2026, time.October, 25, 18, 10, 0, 0, time.UTC), expired: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := repositorytest.Open(t)
			require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1",
				ordertest.WithDeliveryWindow(t, windowStart, windowStart.Add(15*time.Minute)))))
			workflow := NewMockCancellationRequester(t)
			handler := requestordercancellation.NewHandler(requestordercancellation.Dependencies{
				Authorizer: NewMockAuthorizer(t),
				Orders:     store,
				Policy:     fixedWindow(t, 5*time.Minute),
				Clock:      fake.NewClock(tc.requestedAt),
				Workflow:   workflow,
			})
			if !tc.expired {
				workflow.EXPECT().
					RequestCancellation(mock.Anything, fulfillment.CancellationRequest{
						OrderID: "order-1", RequestID: "cancel-1", RequestedBy: customer, RequestedAt: tc.requestedAt, Reason: "changed_mind",
					}).
					Return(nil).
					Once()
			}

			result, err := handler.Handle(context.Background(), input(customer))

			if tc.expired {
				assert.Equal(t, order.CancellationNotAllowed{
					Reasons: []order.PolicyReason{{Code: order.CancellationWindowExpired}},
				}, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, requestordercancellation.OutcomeRequested, result.Outcome)
		})
	}
}
