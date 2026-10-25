package requestordercancellation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/requestordercancellation"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

var customer = application.Subject{Kind: application.SubjectUser, ID: "customer-1"}

type env struct {
	store      *memory.Store
	authorizer *memory.Authorizer
	workflow   *memory.Workflow
	clock      *memory.Clock
	handler    requestordercancellation.Handler
}

func newEnv(t *testing.T, state order.State, policy requestordercancellation.CancellationPolicy) env {
	t.Helper()
	e := env{
		store:      memory.NewStore(),
		authorizer: memory.NewAuthorizer(),
		workflow:   memory.NewWorkflow(),
		clock:      memory.NewClock(ordertest.PlacedAt.Add(5 * time.Minute)),
	}
	ctx := context.Background()
	require.NoError(t, e.store.Add(ctx, ordertest.New(t, "order-1", ordertest.WithState(state))))
	require.NoError(t, e.authorizer.GrantOrderCustomer(ctx, "order-1", "customer-1"))
	e.handler = requestordercancellation.NewHandler(requestordercancellation.Dependencies{
		Authorizer: e.authorizer,
		Orders:     e.store,
		Policy:     policy,
		Clock:      e.clock,
		Workflow:   e.workflow,
	})
	return e
}

func input(subject application.Subject) requestordercancellation.Input {
	return requestordercancellation.Input{Subject: subject, OrderID: "order-1", RequestID: "cancel-1", Reason: "changed_mind"}
}

// AC-116: an admissible request reaches the workflow and leaves the Order unchanged.
func TestRequestCancellationIsHandedToWorkflow(t *testing.T) {
	for _, state := range []order.State{order.StatePlaced, order.StateConfirmed} {
		t.Run(string(state), func(t *testing.T) {
			e := newEnv(t, state, memory.AlwaysOpen{})

			result, err := e.handler.Handle(context.Background(), input(customer))

			require.NoError(t, err)
			assert.Equal(t, requestordercancellation.Result{Outcome: requestordercancellation.OutcomeRequested}, result)
			assert.Equal(t, []fulfillment.CancellationRequest{{
				OrderID:     "order-1",
				RequestID:   "cancel-1",
				RequestedBy: customer,
				RequestedAt: e.clock.Now(),
				Reason:      "changed_mind",
			}}, e.workflow.Cancellations())
			snapshot, _ := e.store.Snapshot("order-1")
			assert.Equal(t, state, snapshot.State)
			assert.Empty(t, e.store.OutboxRecords())
		})
	}
}

// AC-115: authorization is checked first and fails closed.
func TestRequestCancellationRequiresPermission(t *testing.T) {
	stranger := application.Subject{Kind: application.SubjectUser, ID: "customer-2"}
	tests := []struct {
		name    string
		subject application.Subject
		failure error
	}{
		{name: "another customer", subject: stranger},
		{name: "authorization unavailable", subject: customer, failure: errors.New("spicedb timeout")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, order.StatePlaced, memory.AlwaysOpen{})
			e.authorizer.FailChecks(tt.failure)

			_, err := e.handler.Handle(context.Background(), input(tt.subject))

			require.ErrorIs(t, err, application.ErrPermissionDenied)
			assert.Empty(t, e.workflow.Cancellations())
		})
	}

	// A denied subject learns nothing about a missing Order either.
	e := newEnv(t, order.StatePlaced, memory.AlwaysOpen{})
	missing := input(stranger)
	missing.OrderID = "missing"
	_, err := e.handler.Handle(context.Background(), missing)
	require.ErrorIs(t, err, application.ErrPermissionDenied)
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
			policy: memory.AlwaysOpen{},
			check: func(t *testing.T, err error) {
				var invalid order.InvalidTransition
				require.ErrorAs(t, err, &invalid)
			},
		},
		{
			name:   "window expired",
			state:  order.StateConfirmed,
			policy: memory.FixedWindow{Duration: time.Minute},
			check: func(t *testing.T, err error) {
				assert.Equal(t, order.CancellationNotAllowed{
					Reasons: []order.PolicyReason{{Code: order.CancellationWindowExpired}},
				}, err)
			},
		},
		{
			name:    "already cancelled",
			state:   order.StateCancelled,
			policy:  memory.AlwaysOpen{},
			outcome: requestordercancellation.OutcomeAlreadyCancelled,
			check:   func(t *testing.T, err error) { require.NoError(t, err) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.state, tt.policy)

			result, err := e.handler.Handle(context.Background(), input(customer))

			tt.check(t, err)
			assert.Equal(t, tt.outcome, result.Outcome)
			assert.Empty(t, e.workflow.Cancellations())
		})
	}
}

func TestRequestCancellationReportsWorkflowRefusalAndInvalidInput(t *testing.T) {
	e := newEnv(t, order.StatePlaced, memory.AlwaysOpen{})
	refusal := errors.New("already picked up")
	e.workflow.FailCancellation(refusal)

	_, err := e.handler.Handle(context.Background(), input(customer))
	require.ErrorIs(t, err, refusal)

	noRequestID := input(customer)
	noRequestID.RequestID = ""
	_, err = e.handler.Handle(context.Background(), noRequestID)
	require.ErrorIs(t, err, application.ErrInvalidInput)
	assert.Len(t, e.authorizer.Checks(), 1, "invalid input is rejected before authorization")
}
