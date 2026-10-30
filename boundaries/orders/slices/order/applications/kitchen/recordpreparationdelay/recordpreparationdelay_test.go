package recordpreparationdelay_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/kitchen/confirmorder"
	"nomnom/orders/slices/order/applications/kitchen/recordpreparationdelay"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func delay(eventID string, readyAt time.Time) recordpreparationdelay.Input {
	return recordpreparationdelay.Input{EventID: eventID, OrderID: "order-1", OccurredAt: occurredAt.Add(5 * time.Minute), ExpectedReadyAt: readyAt}
}

// AC-771: after RestaurantOrderAccepted a PreparationDelayed with a later time
// replaces Order.expected_ready_at; the workflow gets the new time too.
func TestDelayReplacesExpectedReadyTime(t *testing.T) {
	store := repositorytest.Open(t)
	workflow := NewMockNotifier(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	accepted, delayed := occurredAt.Add(12*time.Minute), occurredAt.Add(20*time.Minute)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "accepted", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
	}).Return(nil).Once()
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "delayed", Kind: fulfillment.KindPreparationDelayed,
		OccurredAt: occurredAt.Add(5 * time.Minute), ExpectedReadyAt: delayed,
	}).Return(nil).Once()
	_, err := confirmorder.NewHandler(store, store, store, workflow).Handle(context.Background(), confirmorder.Input{
		EventID: "accepted", OrderID: "order-1", OccurredAt: occurredAt, ExpectedReadyAt: accepted,
	})
	require.NoError(t, err)

	result, err := recordpreparationdelay.NewHandler(store, store, store, workflow).Handle(context.Background(), delay("delayed", delayed))

	require.NoError(t, err)
	assert.Equal(t, recordpreparationdelay.Result{Outcome: fulfillment.OutcomeApplied, WorkflowNotified: true}, result)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.ExpectedTimes{ReadyAt: delayed}, snapshot.Expected)
	assert.Equal(t, order.StateConfirmed, snapshot.State)
}

func TestDelayOfFinalOrderIsLate(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(order.StateCompleted))))
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationDelayed,
		OccurredAt: occurredAt.Add(5 * time.Minute), ExpectedReadyAt: occurredAt.Add(time.Hour),
	}).Return(nil).Once()

	result, err := recordpreparationdelay.NewHandler(store, store, store, workflow).Handle(context.Background(), delay("event-1", occurredAt.Add(time.Hour)))

	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeIgnoredLate, result.Outcome)
	snapshot, _ := store.Snapshot("order-1")
	assert.Zero(t, snapshot.Expected)
	assert.Equal(t, 1, store.InboxSize())
}

func TestDuplicateDelayIsNotAppliedAgain(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPreparationDelayed,
		OccurredAt: occurredAt.Add(5 * time.Minute), ExpectedReadyAt: occurredAt.Add(time.Hour),
	}).Return(nil).Times(2)
	handler := recordpreparationdelay.NewHandler(store, store, store, workflow)
	_, err := handler.Handle(context.Background(), delay("event-1", occurredAt.Add(time.Hour)))
	require.NoError(t, err)

	result, err := handler.Handle(context.Background(), delay("event-1", occurredAt.Add(time.Hour)))

	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeDuplicate, result.Outcome)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, uint64(2), snapshot.Version)
}

func TestDelayRequiresExpectedReadyTime(t *testing.T) {
	_, err := recordpreparationdelay.NewHandler(nil, nil, nil, NewMockNotifier(t)).Handle(context.Background(), delay("event-1", time.Time{}))

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}
