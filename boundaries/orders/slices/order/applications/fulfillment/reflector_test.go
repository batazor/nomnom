package fulfillment_test

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
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/mocks"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

type harness struct {
	store    *repositorytest.Store
	workflow *mocks.MockNotifier
}

func newHarness(t *testing.T, seeded ...*order.Order) harness {
	t.Helper()
	h := harness{store: repositorytest.Open(t), workflow: mocks.NewMockNotifier(t)}
	for _, o := range seeded {
		require.NoError(t, h.store.Add(context.Background(), o))
	}
	return h
}

func (h harness) reflector(orders order.Repository) fulfillment.Reflector {
	if orders == nil {
		orders = h.store
	}
	return fulfillment.NewReflector(h.store, h.store, orders, h.workflow)
}

func pickup(eventID string) fulfillment.InboundFact {
	return fulfillment.InboundFact{
		Consumer:   "orders.recordpickup",
		EventID:    eventID,
		OrderID:    "order-1",
		Kind:       fulfillment.KindPickedUp,
		OccurredAt: occurredAt,
	}
}

func recordPickup(current *order.Order) error {
	_, err := current.RecordPickup(occurredAt)
	return err
}

// AC-106: the same event_id changes nothing and re-notifies the workflow.
func TestDuplicateEventIsNoOpAndNotifiesAgain(t *testing.T) {
	h := newHarness(t, ordertest.New(t, "order-1"))
	// The duplicate notifies the workflow again with the same notification.
	h.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
		}).
		Return(nil).
		Times(2)
	reflector := h.reflector(nil)

	first, err := reflector.Reflect(context.Background(), pickup("event-1"), recordPickup)
	require.NoError(t, err)
	second, err := reflector.Reflect(context.Background(), pickup("event-1"), recordPickup)
	require.NoError(t, err)

	assert.Equal(t, fulfillment.Reflection{Outcome: fulfillment.OutcomeApplied, WorkflowNotified: true}, first)
	assert.Equal(t, fulfillment.Reflection{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, second)
	snapshot, _ := h.store.Snapshot("order-1")
	assert.Equal(t, uint64(2), snapshot.Version, "the duplicate must not save the Order")
	assert.Equal(t, 1, h.store.InboxSize())
}

// AC-107: the same fact under a new event_id has no second business effect.
func TestRepeatedFactWithNewEventIDIsAlreadyApplied(t *testing.T) {
	h := newHarness(t, ordertest.New(t, "order-1"))
	h.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
		}).
		Return(nil).
		Once()
	h.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-2", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
		}).
		Return(nil).
		Once()
	reflector := h.reflector(nil)

	_, err := reflector.Reflect(context.Background(), pickup("event-1"), recordPickup)
	require.NoError(t, err)
	repeated, err := reflector.Reflect(context.Background(), pickup("event-2"), recordPickup)
	require.NoError(t, err)

	assert.Equal(t, fulfillment.OutcomeAlreadyApplied, repeated.Outcome)
	snapshot, _ := h.store.Snapshot("order-1")
	assert.Equal(t, uint64(2), snapshot.Version)
	assert.Len(t, snapshot.Facts, 1)
	assert.Equal(t, 2, h.store.InboxSize())
}

// AC-112: a fact for a final Order is recorded in the Inbox and ignored.
func TestLateFactForFinalOrderIsIgnored(t *testing.T) {
	for _, state := range []order.State{order.StateCompleted, order.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			h := newHarness(t, ordertest.New(t, "order-1", ordertest.WithState(state)))
			// The workflow of a final Order is closed.
			h.workflow.EXPECT().
				Notify(mock.Anything, fulfillment.Notification{
					OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
				}).
				Return(fulfillment.ErrWorkflowNotFound).
				Once()

			reflection, err := h.reflector(nil).Reflect(context.Background(), pickup("event-1"), recordPickup)

			require.NoError(t, err)
			assert.Equal(t, fulfillment.Reflection{Outcome: fulfillment.OutcomeIgnoredLate}, reflection)
			snapshot, _ := h.store.Snapshot("order-1")
			assert.Empty(t, snapshot.Facts)
			assert.Equal(t, uint64(1), snapshot.Version)
			assert.Equal(t, 1, h.store.InboxSize(), "redelivery must become a duplicate")
		})
	}
}

// AC-113: a failed notification keeps the commit and is repaired by redelivery.
func TestFailedNotificationIsReturnedAndRepairedByRedelivery(t *testing.T) {
	h := newHarness(t, ordertest.New(t, "order-1"))
	reflector := h.reflector(nil)
	h.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
		}).
		Return(errors.New("temporal unavailable")).
		Once()
	h.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
		}).
		Return(nil).
		Once()

	reflection, err := reflector.Reflect(context.Background(), pickup("event-1"), recordPickup)

	require.Error(t, err)
	assert.Equal(t, fulfillment.Reflection{Outcome: fulfillment.OutcomeApplied}, reflection)
	snapshot, _ := h.store.Snapshot("order-1")
	assert.Len(t, snapshot.Facts, 1, "local changes stay committed")
	assert.Equal(t, 1, h.store.InboxSize())

	redelivered, err := reflector.Reflect(context.Background(), pickup("event-1"), recordPickup)
	require.NoError(t, err)
	assert.Equal(t, fulfillment.Reflection{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, redelivered)
}

// AC-114 and AC-130: a failed transaction leaves no Inbox, Order or notification.
func TestFailedTransactionRollsBackWithoutNotification(t *testing.T) {
	t.Run("save fails", func(t *testing.T) {
		// The notifier has no expectation: a notification fails the test.
		h := newHarness(t, ordertest.New(t, "order-1"))
		before, _ := h.store.Snapshot("order-1")
		saveFailure := errors.New("disk full")
		orders := NewMockRepository(t)
		orders.EXPECT().Get(mock.Anything, order.ID("order-1")).RunAndReturn(h.store.Get).Once()
		orders.EXPECT().
			Save(mock.Anything, mock.MatchedBy(func(o *order.Order) bool { return o.ID() == "order-1" })).
			Return(saveFailure).
			Once()

		_, err := h.reflector(orders).Reflect(context.Background(), pickup("event-1"), recordPickup)

		require.ErrorIs(t, err, saveFailure)
		after, _ := h.store.Snapshot("order-1")
		assert.Equal(t, before, after)
		assert.Zero(t, h.store.InboxSize())
	})
	t.Run("concurrent change", func(t *testing.T) {
		// The notifier has no expectation: a notification fails the test.
		h := newHarness(t, ordertest.New(t, "order-1"))
		before, _ := h.store.Snapshot("order-1")
		orders := NewMockRepository(t)
		// The same Order is changed between load and save of the handler in
		// every attempt: the unit of work repeats the conflict three times.
		orders.EXPECT().
			Get(mock.Anything, order.ID("order-1")).
			RunAndReturn(func(ctx context.Context, id order.ID) (*order.Order, error) {
				loaded, err := h.store.Get(ctx, id)
				if err != nil {
					return nil, err
				}
				other, err := h.store.Get(ctx, id)
				if err != nil {
					return nil, err
				}
				if _, err := other.Confirm(ordertest.PlacedAt.Add(time.Minute)); err != nil {
					return nil, err
				}
				return loaded, h.store.Save(ctx, other)
			}).
			Times(3)
		orders.EXPECT().
			Save(mock.Anything, mock.MatchedBy(func(o *order.Order) bool { return o.ID() == "order-1" })).
			RunAndReturn(h.store.Save).
			Times(3)

		_, err := h.reflector(orders).Reflect(context.Background(), pickup("event-1"), recordPickup)

		require.ErrorIs(t, err, application.ErrConcurrencyConflict)
		after, _ := h.store.Snapshot("order-1")
		assert.Equal(t, before, after)
		assert.Zero(t, h.store.InboxSize())
	})
}

func TestReflectRejectsInvalidInputAndUnknownOrders(t *testing.T) {
	// The notifier has no expectation: a notification fails the test.
	h := newHarness(t, ordertest.New(t, "order-1"))
	reflector := h.reflector(nil)

	invalid := []fulfillment.InboundFact{
		{EventID: "e", OrderID: "order-1", OccurredAt: occurredAt},
		{Consumer: "c", OrderID: "order-1", OccurredAt: occurredAt},
		{Consumer: "c", EventID: "e", OccurredAt: occurredAt},
		{Consumer: "c", EventID: "e", OrderID: "order-1"},
	}
	for _, fact := range invalid {
		_, err := reflector.Reflect(context.Background(), fact, recordPickup)
		require.ErrorIs(t, err, application.ErrInvalidInput)
	}

	unknown := pickup("event-1")
	unknown.OrderID = "missing"
	_, err := reflector.Reflect(context.Background(), unknown, recordPickup)
	require.ErrorIs(t, err, order.ErrOrderNotFound)
	assert.Zero(t, h.store.InboxSize())

	// InvalidTransition from a non-final state is a defect, not a late event.
	_, err = reflector.Reflect(context.Background(), pickup("event-2"), func(current *order.Order) error {
		_, err := current.Complete(time.Now())
		return err
	})
	var invalidTransition order.InvalidTransition
	require.ErrorAs(t, err, &invalidTransition)
	assert.Zero(t, h.store.InboxSize())
}
