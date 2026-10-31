package recordpreparationstarted_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordpreparationstarted"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// AC-113: a failed notification keeps the commit and is repaired by redelivery.
func TestFailedNotificationIsReturnedAndRepairedByRedelivery(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	before, _ := store.Snapshot("order-1")
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationStarted, OccurredAt: occurredAt}).Return(errors.New("temporal unavailable")).Once()
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationStarted, OccurredAt: occurredAt}).Return(nil).Once()
	handler := recordpreparationstarted.NewHandler(store, store, store, workflow)

	result, err := handler.Handle(context.Background(), input("event-1"))

	require.Error(t, err)
	assert.Equal(t, recordpreparationstarted.Result{Outcome: fulfillment.OutcomeApplied}, result)
	after, _ := store.Snapshot("order-1")
	assert.Equal(t, before.Version+1, after.Version, "local changes stay committed")
	assert.Equal(t, 1, store.InboxSize())

	redelivered, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)
	assert.Equal(t, recordpreparationstarted.Result{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, redelivered)
}

// AC-114: a failed save leaves no Inbox record, Order change or notification.
func TestFailedSaveRollsBackWithoutNotification(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	before, _ := store.Snapshot("order-1")
	saveFailure := errors.New("disk full")
	orders := NewMockRepository(t)
	orders.EXPECT().Get(mock.Anything, order.ID("order-1")).RunAndReturn(store.Get).Once()
	orders.EXPECT().
		Save(mock.Anything, mock.MatchedBy(func(o *order.Order) bool { return o.ID() == "order-1" })).
		Return(saveFailure).
		Once()
	// The notifier has no expectation: a notification fails the test.
	workflow := NewMockNotifier(t)

	_, err := recordpreparationstarted.NewHandler(store, store, orders, workflow).Handle(context.Background(), input("event-1"))

	require.ErrorIs(t, err, saveFailure)
	after, _ := store.Snapshot("order-1")
	assert.Equal(t, before, after)
	assert.Zero(t, store.InboxSize())
}

func TestRejectsInvalidInputAndUnknownOrder(t *testing.T) {
	store := repositorytest.Open(t)
	// The notifier has no expectation: a notification fails the test.
	handler := recordpreparationstarted.NewHandler(store, store, store, NewMockNotifier(t))

	for _, in := range []recordpreparationstarted.Input{
		{OrderID: "order-1", OccurredAt: occurredAt},
		{EventID: "event-1", OccurredAt: occurredAt},
		{EventID: "event-1", OrderID: "order-1"},
	} {
		_, err := handler.Handle(context.Background(), in)
		require.ErrorIs(t, err, usecase.ErrInvalidInput)
	}

	_, err := handler.Handle(context.Background(), recordpreparationstarted.Input{EventID: "event-1", OrderID: "missing", OccurredAt: occurredAt})
	require.ErrorIs(t, err, order.ErrOrderNotFound)
	assert.Zero(t, store.InboxSize())
}
