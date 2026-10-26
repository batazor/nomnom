package recordcourierassignment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func input(eventID string) recordcourierassignment.Input {
	return recordcourierassignment.Input{EventID: eventID, OrderID: "order-1", OccurredAt: occurredAt}
}

// AC-110 (recordcourierassignment.feature @AC-002): the fact is recorded once in a non-final Order without changing its state.
func TestHandleByOrderState(t *testing.T) {
	recorded := order.RecordedFact{Fact: order.FactCourierAssigned, OccurredAt: occurredAt}
	tests := []struct {
		name      string
		options   []ordertest.Option
		outcome   fulfillment.Outcome
		wantState order.State
		wantFacts []order.RecordedFact
	}{
		{name: "placed", outcome: fulfillment.OutcomeApplied, wantState: order.StatePlaced, wantFacts: []order.RecordedFact{recorded}},
		{
			name:      "already recorded",
			options:   []ordertest.Option{ordertest.WithFacts(recorded)},
			outcome:   fulfillment.OutcomeAlreadyApplied,
			wantState: order.StatePlaced,
			wantFacts: []order.RecordedFact{recorded},
		},
		{
			name:      "cancelled is late",
			options:   []ordertest.Option{ordertest.WithState(order.StateCancelled)},
			outcome:   fulfillment.OutcomeIgnoredLate,
			wantState: order.StateCancelled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := repositorytest.Open(t)
			workflow := fake.NewWorkflow()
			require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", tt.options...)))
			handler := recordcourierassignment.NewHandler(store, store, store, workflow, store, store)

			result, err := handler.Handle(context.Background(), input("event-1"))

			require.NoError(t, err)
			assert.Equal(t, recordcourierassignment.Result{Outcome: tt.outcome, WorkflowNotified: true}, result)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, tt.wantState, snapshot.State)
			assert.Equal(t, tt.wantFacts, snapshot.Facts)
			assert.Equal(t, []fulfillment.Notification{{
				OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: occurredAt,
			}}, workflow.Notifications())
			assert.Equal(t, 1, store.InboxSize())
		})
	}
}

func assignment(eventID, courierID string, version uint64, at time.Time) recordcourierassignment.Input {
	return recordcourierassignment.Input{
		EventID: eventID, OrderID: "order-1", OccurredAt: at, DeliveryVersion: version, DeliveryID: "delivery-1", CourierID: courierID,
	}
}

func assignmentOn(eventID, deliveryID, courierID string, version uint64, at time.Time) recordcourierassignment.Input {
	in := assignment(eventID, courierID, version, at)
	in.DeliveryID = deliveryID
	return in
}

// linkedDelivery is the stored Delivery of the Order: the subject of
// order#delivery that Sync Order Relationships writes (ADR 18).
func linkedDelivery(t *testing.T, store *repositorytest.Store) string {
	t.Helper()
	stored, _, err := store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	return stored.DeliveryID
}

// AC-518: the assignment stores the Delivery and records RelationshipsChanged
// in the same transaction; orders-consumer then syncs order#delivery.
func TestHandleLinksDelivery(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	handler := recordcourierassignment.NewHandler(store, store, store, fake.NewWorkflow(), store, store)

	_, err := handler.Handle(context.Background(), assignment("event-1", "courier-1", 2, occurredAt))

	require.NoError(t, err)
	assert.Equal(t, "delivery-1", linkedDelivery(t, store))
	assert.Len(t, store.RelationshipChanges(), 1)
	stored, found, err := store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: occurredAt, DeliveryVersion: 2}, stored)
}

// AC-519: a reassignment within the same Delivery updates the assignment
// without RelationshipsChanged; another Delivery records one.
func TestHandleReassignment(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	handler := recordcourierassignment.NewHandler(store, store, store, fake.NewWorkflow(), store, store)
	_, err := handler.Handle(context.Background(), assignment("event-1", "courier-1", 2, occurredAt))
	require.NoError(t, err)

	result, err := handler.Handle(context.Background(), assignment("event-2", "courier-2", 3, occurredAt.Add(time.Minute)))

	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeAlreadyApplied, result.Outcome)
	assert.Len(t, store.RelationshipChanges(), 1, "the linked Delivery is unchanged")
	stored, _, err := store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-2", AssignedAt: occurredAt.Add(time.Minute), DeliveryVersion: 3}, stored)

	_, err = handler.Handle(context.Background(), assignmentOn("event-3", "delivery-2", "courier-3", 4, occurredAt.Add(2*time.Minute)))

	require.NoError(t, err)
	assert.Len(t, store.RelationshipChanges(), 2)
	assert.Equal(t, "delivery-2", linkedDelivery(t, store))
}

// AC-520, AC-669: an event of an older Delivery version than the stored
// assignment changes nothing, even if its occurred_at (the start of the
// delivery transaction) is later.
func TestHandleSkipsOutOfOrderAssignment(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	handler := recordcourierassignment.NewHandler(store, store, store, fake.NewWorkflow(), store, store)

	_, err := handler.Handle(context.Background(), assignment("event-2", "courier-2", 3, occurredAt))
	require.NoError(t, err)
	_, err = handler.Handle(context.Background(), assignmentOn("event-1", "delivery-0", "courier-1", 2, occurredAt.Add(time.Minute)))
	require.NoError(t, err)

	assert.Len(t, store.RelationshipChanges(), 1)
	assert.Equal(t, "delivery-1", linkedDelivery(t, store))
	stored, _, err := store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Equal(t, "courier-2", stored.CourierID)
}

// failingChanges fails RelationshipsChanged until healed, then records it in
// the store.
type failingChanges struct {
	*repositorytest.Store
	failing bool
}

func (c *failingChanges) RelationshipsChanged(ctx context.Context, orderID order.ID) error {
	if c.failing {
		return errors.New("outbox unavailable")
	}
	return c.Store.RelationshipsChanged(ctx, orderID)
}

// AC-521: RelationshipsChanged commits with the assignment: a failure rolls
// the Inbox record and the assignment back, so redelivery repeats the event.
func TestHandleRollsBackWithRelationshipsChange(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	changes := &failingChanges{Store: store, failing: true}
	workflow := fake.NewWorkflow()
	handler := recordcourierassignment.NewHandler(store, store, store, workflow, store, changes)

	_, err := handler.Handle(context.Background(), assignment("event-1", "courier-1", 2, occurredAt))
	require.Error(t, err)
	assert.Equal(t, 0, store.InboxSize())
	snapshot, _ := store.Snapshot("order-1")
	assert.Empty(t, snapshot.Facts)
	assert.Empty(t, workflow.Notifications())
	assert.Empty(t, linkedDelivery(t, store))

	changes.failing = false
	result, err := handler.Handle(context.Background(), assignment("event-1", "courier-1", 2, occurredAt))
	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeApplied, result.Outcome)
	assert.Equal(t, "delivery-1", linkedDelivery(t, store))
	assert.Equal(t, []order.ID{"order-1"}, store.RelationshipChanges())
}

// FR-611: the assignment stores the pickup time named by the courier; a newer
// reassignment replaces it and saves the Order, an out-of-order one does not.
func TestHandleReplacesExpectedPickupTime(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	handler := recordcourierassignment.NewHandler(store, store, store, fake.NewWorkflow(), store, store)
	expecting := func(in recordcourierassignment.Input, pickupAt time.Time) recordcourierassignment.Input {
		in.ExpectedPickupAt = pickupAt
		return in
	}
	first, second := occurredAt.Add(10*time.Minute), occurredAt.Add(15*time.Minute)

	_, err := handler.Handle(context.Background(), expecting(assignment("event-1", "courier-1", 2, occurredAt), first))
	require.NoError(t, err)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.ExpectedTimes{PickupAt: first}, snapshot.Expected)

	result, err := handler.Handle(context.Background(), expecting(assignment("event-2", "courier-2", 4, occurredAt), second))
	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeAlreadyApplied, result.Outcome)
	snapshot, _ = store.Snapshot("order-1")
	assert.Equal(t, order.ExpectedTimes{PickupAt: second}, snapshot.Expected)
	assert.Equal(t, uint64(3), snapshot.Version, "the reassignment saves the Order")

	_, err = handler.Handle(context.Background(), expecting(assignment("event-3", "courier-1", 3, occurredAt), first))
	require.NoError(t, err)
	snapshot, _ = store.Snapshot("order-1")
	assert.Equal(t, order.ExpectedTimes{PickupAt: second}, snapshot.Expected, "an older Delivery version changes nothing")
	assert.Equal(t, uint64(3), snapshot.Version)
}
