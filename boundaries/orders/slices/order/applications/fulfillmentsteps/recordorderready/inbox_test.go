package recordorderready_test

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
	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordorderready"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// AC-106: the same event_id changes nothing and re-notifies the workflow.
func TestDuplicateEventIsNoOpAndNotifiesAgain(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindReadyForPickup, OccurredAt: occurredAt}).Return(nil).Times(2)
	handler := recordorderready.NewHandler(store, store, store, workflow)

	first, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)
	second, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)

	assert.Equal(t, recordorderready.Result{Outcome: fulfillment.OutcomeApplied, WorkflowNotified: true}, first)
	assert.Equal(t, recordorderready.Result{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, second)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, uint64(2), snapshot.Version, "the duplicate must not save the Order")
	assert.Equal(t, 1, store.InboxSize())
}

// AC-112: a signal for a final Order is recorded in the Inbox and ignored; the
// workflow of a final Order is closed.
func TestLateSignalForFinalOrderIsIgnored(t *testing.T) {
	for _, state := range []order.State{order.StateCompleted, order.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			store := repositorytest.Open(t)
			require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(state))))
			workflow := NewMockNotifier(t)
			workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindReadyForPickup, OccurredAt: occurredAt}).Return(fulfillment.ErrWorkflowNotFound).Once()

			result, err := recordorderready.NewHandler(store, store, store, workflow).Handle(context.Background(), input("event-1"))

			require.NoError(t, err)
			assert.Equal(t, recordorderready.Result{Outcome: fulfillment.OutcomeIgnoredLate}, result)
			snapshot, _ := store.Snapshot("order-1")
			assert.Empty(t, snapshot.Transitions)
			assert.Equal(t, uint64(1), snapshot.Version)
			assert.Equal(t, 1, store.InboxSize(), "redelivery must become a duplicate")
		})
	}
}

// AC-113: a failed notification keeps the commit and is repaired by redelivery.
func TestFailedNotificationIsReturnedAndRepairedByRedelivery(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	before, _ := store.Snapshot("order-1")
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindReadyForPickup, OccurredAt: occurredAt}).Return(errors.New("temporal unavailable")).Once()
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindReadyForPickup, OccurredAt: occurredAt}).Return(nil).Once()
	handler := recordorderready.NewHandler(store, store, store, workflow)

	result, err := handler.Handle(context.Background(), input("event-1"))

	require.Error(t, err)
	assert.Equal(t, recordorderready.Result{Outcome: fulfillment.OutcomeApplied}, result)
	after, _ := store.Snapshot("order-1")
	assert.Equal(t, before.Version+1, after.Version, "local changes stay committed")
	assert.Equal(t, 1, store.InboxSize())

	redelivered, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)
	assert.Equal(t, recordorderready.Result{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, redelivered)
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

	_, err := recordorderready.NewHandler(store, store, orders, workflow).Handle(context.Background(), input("event-1"))

	require.ErrorIs(t, err, saveFailure)
	after, _ := store.Snapshot("order-1")
	assert.Equal(t, before, after)
	assert.Zero(t, store.InboxSize())
}

// AC-130: a concurrent change of the Order rolls every attempt back.
func TestConcurrentChangeRollsBackWithoutNotification(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	before, _ := store.Snapshot("order-1")
	orders := NewMockRepository(t)
	// The same Order is changed between load and save of the handler in every
	// attempt: the unit of work repeats the conflict three times.
	orders.EXPECT().
		Get(mock.Anything, order.ID("order-1")).
		RunAndReturn(func(ctx context.Context, id order.ID) (*order.Order, error) {
			loaded, err := store.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			other, err := store.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			if _, err := other.Confirm(ordertest.PlacedAt.Add(time.Minute)); err != nil {
				return nil, err
			}
			return loaded, store.Save(ctx, other)
		}).
		Times(3)
	orders.EXPECT().
		Save(mock.Anything, mock.MatchedBy(func(o *order.Order) bool { return o.ID() == "order-1" })).
		RunAndReturn(store.Save).
		Times(3)
	// The notifier has no expectation: a notification fails the test.
	workflow := NewMockNotifier(t)

	_, err := recordorderready.NewHandler(store, store, orders, workflow).Handle(context.Background(), input("event-1"))

	require.ErrorIs(t, err, usecase.ErrConcurrencyConflict)
	after, _ := store.Snapshot("order-1")
	assert.Equal(t, before, after)
	assert.Zero(t, store.InboxSize())
}

func TestRejectsInvalidInputAndUnknownOrder(t *testing.T) {
	store := repositorytest.Open(t)
	// The notifier has no expectation: a notification fails the test.
	handler := recordorderready.NewHandler(store, store, store, NewMockNotifier(t))

	for _, in := range []recordorderready.Input{
		{OrderID: "order-1", OccurredAt: occurredAt},
		{EventID: "event-1", OccurredAt: occurredAt},
		{EventID: "event-1", OrderID: "order-1"},
	} {
		_, err := handler.Handle(context.Background(), in)
		require.ErrorIs(t, err, usecase.ErrInvalidInput)
	}

	_, err := handler.Handle(context.Background(), recordorderready.Input{EventID: "event-1", OrderID: "missing", OccurredAt: occurredAt})
	require.ErrorIs(t, err, order.ErrOrderNotFound)
	assert.Zero(t, store.InboxSize())
}
