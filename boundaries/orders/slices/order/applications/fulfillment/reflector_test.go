package fulfillment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

type harness struct {
	store    *memory.Store
	workflow *memory.Workflow
}

func newHarness(t *testing.T, seeded ...*order.Order) harness {
	t.Helper()
	h := harness{store: memory.NewStore(), workflow: memory.NewWorkflow()}
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
	notifications := h.workflow.Notifications()
	require.Len(t, notifications, 2)
	assert.Equal(t, notifications[0], notifications[1])
	assert.Equal(t, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
	}, notifications[0])
}

// AC-107: the same fact under a new event_id has no second business effect.
func TestRepeatedFactWithNewEventIDIsAlreadyApplied(t *testing.T) {
	h := newHarness(t, ordertest.New(t, "order-1"))
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
	require.Len(t, h.workflow.Notifications(), 2)
	assert.Equal(t, "event-2", h.workflow.Notifications()[1].EventID)
}

// AC-112: a fact for a final Order is recorded in the Inbox and ignored.
func TestLateFactForFinalOrderIsIgnored(t *testing.T) {
	for _, state := range []order.State{order.StateCompleted, order.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			h := newHarness(t, ordertest.New(t, "order-1", ordertest.WithState(state)))
			h.workflow.Close("order-1")

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
	h.workflow.FailNotify(errors.New("temporal unavailable"))

	reflection, err := reflector.Reflect(context.Background(), pickup("event-1"), recordPickup)

	require.Error(t, err)
	assert.Equal(t, fulfillment.Reflection{Outcome: fulfillment.OutcomeApplied}, reflection)
	snapshot, _ := h.store.Snapshot("order-1")
	assert.Len(t, snapshot.Facts, 1, "local changes stay committed")
	assert.Equal(t, 1, h.store.InboxSize())

	h.workflow.FailNotify(nil)
	redelivered, err := reflector.Reflect(context.Background(), pickup("event-1"), recordPickup)
	require.NoError(t, err)
	assert.Equal(t, fulfillment.Reflection{Outcome: fulfillment.OutcomeDuplicate, WorkflowNotified: true}, redelivered)
	require.Len(t, h.workflow.Notifications(), 1)
}

type failingSave struct {
	order.Repository
	err error
}

func (r failingSave) Save(context.Context, *order.Order) error { return r.err }

// concurrentWriter changes the same Order between load and save of the handler.
type concurrentWriter struct {
	*memory.Store
}

func (r concurrentWriter) Get(ctx context.Context, id order.ID) (*order.Order, error) {
	loaded, err := r.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	other, err := r.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := other.Confirm(); err != nil {
		return nil, err
	}
	return loaded, r.Save(ctx, other)
}

// AC-114 and AC-130: a failed transaction leaves no Inbox, Order or notification.
func TestFailedTransactionRollsBackWithoutNotification(t *testing.T) {
	saveFailure := errors.New("disk full")
	tests := []struct {
		name    string
		orders  func(*memory.Store) order.Repository
		wantErr error
	}{
		{
			name:    "save fails",
			orders:  func(store *memory.Store) order.Repository { return failingSave{Repository: store, err: saveFailure} },
			wantErr: saveFailure,
		},
		{
			name:    "concurrent change",
			orders:  func(store *memory.Store) order.Repository { return concurrentWriter{Store: store} },
			wantErr: application.ErrConcurrencyConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, ordertest.New(t, "order-1"))
			before, _ := h.store.Snapshot("order-1")

			_, err := h.reflector(tt.orders(h.store)).Reflect(context.Background(), pickup("event-1"), recordPickup)

			require.ErrorIs(t, err, tt.wantErr)
			after, _ := h.store.Snapshot("order-1")
			assert.Equal(t, before, after)
			assert.Zero(t, h.store.InboxSize())
			assert.Empty(t, h.workflow.Notifications())
		})
	}
}

func TestReflectRejectsInvalidInputAndUnknownOrders(t *testing.T) {
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
		_, err := current.Complete()
		return err
	})
	var invalidTransition order.InvalidTransition
	require.ErrorAs(t, err, &invalidTransition)
	assert.Zero(t, h.store.InboxSize())
	assert.Empty(t, h.workflow.Notifications())
}
