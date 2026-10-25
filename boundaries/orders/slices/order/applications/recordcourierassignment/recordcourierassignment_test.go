package recordcourierassignment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
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
			name:      "confirmed",
			options:   []ordertest.Option{ordertest.WithState(order.StateConfirmed)},
			outcome:   fulfillment.OutcomeApplied,
			wantState: order.StateConfirmed,
			wantFacts: []order.RecordedFact{recorded},
		},
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
		{
			name:      "completed is late",
			options:   []ordertest.Option{ordertest.WithState(order.StateCompleted)},
			outcome:   fulfillment.OutcomeIgnoredLate,
			wantState: order.StateCompleted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := memory.NewStore()
			workflow := memory.NewWorkflow()
			require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", tt.options...)))
			handler := recordcourierassignment.NewHandler(store, store, store, workflow, store, memory.NewAuthorizer())

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

func TestHandleRedeliveryIsDuplicate(t *testing.T) {
	store := memory.NewStore()
	workflow := memory.NewWorkflow()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	handler := recordcourierassignment.NewHandler(store, store, store, workflow, store, memory.NewAuthorizer())

	_, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)
	result, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)

	assert.Equal(t, fulfillment.OutcomeDuplicate, result.Outcome)
	snapshot, _ := store.Snapshot("order-1")
	assert.Len(t, snapshot.Facts, 1)
	assert.Len(t, workflow.Notifications(), 2)
}

func TestHandleRequiresOccurredAt(t *testing.T) {
	store := memory.NewStore()
	handler := recordcourierassignment.NewHandler(store, store, store, memory.NewWorkflow(), store, memory.NewAuthorizer())

	_, err := handler.Handle(context.Background(), recordcourierassignment.Input{EventID: "event-1", OrderID: "order-1"})

	require.ErrorIs(t, err, application.ErrInvalidInput)
}

func assignment(eventID, courierID string, version uint64, at time.Time) recordcourierassignment.Input {
	return recordcourierassignment.Input{
		EventID: eventID, OrderID: "order-1", OccurredAt: at, DeliveryVersion: version, DeliveryID: "delivery-1", CourierID: courierID,
	}
}

func courierCanView(t *testing.T, authorizer *memory.Authorizer, courierID string) bool {
	t.Helper()
	return authorizer.Check(context.Background(), application.AccessCheck{
		Subject:    application.Subject{Kind: application.SubjectUser, ID: courierID},
		Permission: "view",
		Resource:   application.Resource{Type: "order", ID: "order-1"},
	}) == nil
}

// AC-518, AC-519: the assignment writes the courier relationships before
// commit, and a reassignment replaces the courier although the fact is
// already recorded.
func TestHandleWritesAndReplacesCourierRelationships(t *testing.T) {
	store, authorizer := memory.NewStore(), memory.NewAuthorizer()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	handler := recordcourierassignment.NewHandler(store, store, store, memory.NewWorkflow(), store, authorizer)

	_, err := handler.Handle(context.Background(), assignment("event-1", "courier-1", 2, occurredAt))
	require.NoError(t, err)
	assert.True(t, courierCanView(t, authorizer, "courier-1"))
	stored, found, err := store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: occurredAt, DeliveryVersion: 2}, stored)

	result, err := handler.Handle(context.Background(), assignment("event-2", "courier-2", 3, occurredAt.Add(time.Minute)))
	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeAlreadyApplied, result.Outcome)
	assert.False(t, courierCanView(t, authorizer, "courier-1"))
	assert.True(t, courierCanView(t, authorizer, "courier-2"))
	revision, _, err := store.Revision(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("memory:order-1:courier:2"), revision)
}

// AC-520, AC-669: an event of an older Delivery version than the stored
// assignment changes nothing, even if its occurred_at (the start of the
// delivery transaction) is later.
func TestHandleSkipsOutOfOrderAssignment(t *testing.T) {
	store, authorizer := memory.NewStore(), memory.NewAuthorizer()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	handler := recordcourierassignment.NewHandler(store, store, store, memory.NewWorkflow(), store, authorizer)

	_, err := handler.Handle(context.Background(), assignment("event-2", "courier-2", 3, occurredAt))
	require.NoError(t, err)
	_, err = handler.Handle(context.Background(), assignment("event-1", "courier-1", 2, occurredAt.Add(time.Minute)))
	require.NoError(t, err)

	assert.Equal(t, 1, authorizer.CourierWrites())
	assert.True(t, courierCanView(t, authorizer, "courier-2"))
	assert.False(t, courierCanView(t, authorizer, "courier-1"))
}

// AC-521: a failed relationship write rolls the Inbox record back, so
// redelivery repeats the event.
func TestHandleRollsBackOnRelationshipFailure(t *testing.T) {
	store, authorizer := memory.NewStore(), memory.NewAuthorizer()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	authorizer.FailWrites(errors.New("spicedb unavailable"))
	workflow := memory.NewWorkflow()
	handler := recordcourierassignment.NewHandler(store, store, store, workflow, store, authorizer)

	_, err := handler.Handle(context.Background(), assignment("event-1", "courier-1", 2, occurredAt))
	require.Error(t, err)
	assert.Equal(t, 0, store.InboxSize())
	snapshot, _ := store.Snapshot("order-1")
	assert.Empty(t, snapshot.Facts)
	assert.Empty(t, workflow.Notifications())

	authorizer.FailWrites(nil)
	result, err := handler.Handle(context.Background(), assignment("event-1", "courier-1", 2, occurredAt))
	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeApplied, result.Outcome)
	assert.True(t, courierCanView(t, authorizer, "courier-1"))
}
