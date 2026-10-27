package startfulfillment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/startfulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/mocks"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

func input() startfulfillment.Input {
	return startfulfillment.Input{EventID: "event-1", OrderID: "order-1", OccurredAt: occurredAt}
}

func newHandler(t *testing.T, options ...ordertest.Option) (*repositorytest.Store, *mocks.MockStarter, startfulfillment.Handler) {
	t.Helper()
	store := repositorytest.Open(t)
	workflow := mocks.NewMockStarter(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", options...)))
	return store, workflow, startfulfillment.NewHandler(store, store, store, workflow)
}

var started = fulfillment.StartRequest{OrderID: "order-1", EventID: "event-1", OccurredAt: occurredAt}

// AC-827: the first delivery records the Inbox and starts the workflow once.
func TestStartsWorkflow(t *testing.T) {
	store, workflow, handler := newHandler(t)
	workflow.EXPECT().Start(mock.Anything, started).Return(nil).Once()

	result, err := handler.Handle(context.Background(), input())

	require.NoError(t, err)
	assert.Equal(t, startfulfillment.Result{Outcome: fulfillment.OutcomeApplied, WorkflowNotified: true}, result)
	assert.Equal(t, 1, store.InboxSize())
}

// AC-819: a duplicate starts the workflow again, repairing a crash between
// the Inbox commit and the start.
func TestDuplicateStartsAgain(t *testing.T) {
	store, workflow, handler := newHandler(t)
	workflow.EXPECT().Start(mock.Anything, started).Return(nil).Times(2)
	_, err := handler.Handle(context.Background(), input())
	require.NoError(t, err)

	result, err := handler.Handle(context.Background(), input())

	require.NoError(t, err)
	assert.Equal(t, startfulfillment.Result{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, result)
	assert.Equal(t, 1, store.InboxSize())
}

// AC-820: OrderPlaced of a final Order does not start its workflow again.
func TestFinalOrderIsNotStarted(t *testing.T) {
	for _, state := range []order.State{order.StateCompleted, order.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			// The starter has no expectation: a start fails the test.
			store, _, handler := newHandler(t, ordertest.WithState(state))

			result, err := handler.Handle(context.Background(), input())
			require.NoError(t, err)
			assert.Equal(t, startfulfillment.Result{Outcome: fulfillment.OutcomeIgnoredLate}, result)

			duplicate, err := handler.Handle(context.Background(), input())
			require.NoError(t, err)
			assert.Equal(t, startfulfillment.Result{Outcome: fulfillment.OutcomeIgnoredLate}, duplicate)
			assert.Equal(t, 1, store.InboxSize())
		})
	}
}

// AC-821: an unavailable Temporal is a transient error; the Inbox record stays
// and the redelivery starts the workflow.
func TestStartFailureIsUnavailable(t *testing.T) {
	_, workflow, handler := newHandler(t)
	workflow.EXPECT().Start(mock.Anything, started).Return(errors.New("temporal unavailable")).Once()
	workflow.EXPECT().Start(mock.Anything, started).Return(nil).Once()

	_, err := handler.Handle(context.Background(), input())

	require.ErrorIs(t, err, application.ErrDependencyUnavailable)

	result, err := handler.Handle(context.Background(), input())
	require.NoError(t, err)
	assert.Equal(t, startfulfillment.Result{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, result)
}

// AC-828: an unknown Order is not found and starts nothing.
func TestUnknownOrder(t *testing.T) {
	// The starter has no expectation: a start fails the test.
	_, _, handler := newHandler(t)
	in := input()
	in.OrderID = "order-unknown"

	_, err := handler.Handle(context.Background(), in)

	require.ErrorIs(t, err, order.ErrOrderNotFound)
}

func TestRequiresEvent(t *testing.T) {
	// The starter has no expectation: a start fails the test.
	_, _, handler := newHandler(t)
	in := input()
	in.EventID = ""

	_, err := handler.Handle(context.Background(), in)

	require.ErrorIs(t, err, application.ErrInvalidInput)
}
