package startfulfillment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/startfulfillment"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

func input() startfulfillment.Input {
	return startfulfillment.Input{EventID: "event-1", OrderID: "order-1", OccurredAt: occurredAt}
}

func newHandler(t *testing.T, options ...ordertest.Option) (*repositorytest.Store, *MockStarter, startfulfillment.Handler) {
	t.Helper()
	store := repositorytest.Open(t)
	workflow := NewMockStarter(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", options...)))
	return store, workflow, startfulfillment.NewHandler(store, store, store, workflow)
}

var started = fulfillment.StartRequest{OrderID: "order-1", RestaurantID: "restaurant-1", EventID: "event-1", OccurredAt: occurredAt}

// AC-821: an unavailable Temporal is a transient error; the Inbox record stays
// and the redelivery starts the workflow.
func TestStartFailureIsUnavailable(t *testing.T) {
	_, workflow, handler := newHandler(t)
	workflow.EXPECT().Start(mock.Anything, started).Return(errors.New("temporal unavailable")).Once()
	workflow.EXPECT().Start(mock.Anything, started).Return(nil).Once()

	_, err := handler.Handle(context.Background(), input())

	require.ErrorIs(t, err, usecase.ErrDependencyUnavailable)

	result, err := handler.Handle(context.Background(), input())
	require.NoError(t, err)
	assert.Equal(t, startfulfillment.Result{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, result)
}

func TestRequiresEvent(t *testing.T) {
	// The starter has no expectation: a start fails the test.
	_, _, handler := newHandler(t)
	in := input()
	in.EventID = ""

	_, err := handler.Handle(context.Background(), in)

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}

func TestRejectsHalfOfADeliveryWindow(t *testing.T) {
	store, _, handler := newHandler(t)
	in := input()
	in.DeliveryWindowStart = occurredAt

	_, err := handler.Handle(context.Background(), in)

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
	assert.Zero(t, store.InboxSize())
}
