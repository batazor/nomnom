package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	checkoutapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	tracking "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// assertComplete checks that every application value has a GraphQL value and
// that every GraphQL value but UNSPECIFIED is reachable. names is the
// <Enum>_name map of the generated code.
func assertComplete[A ~string, G ~int32](t *testing.T, table map[A]G, values []A, names map[int32]string) {
	t.Helper()
	reached := map[int32]bool{}
	for _, value := range values {
		graphql, ok := table[value]
		if assert.True(t, ok, "%s has no GraphQL value", value) {
			assert.NotZero(t, graphql, "%s maps to UNSPECIFIED", value)
			reached[int32(graphql)] = true
		}
	}
	assert.Len(t, table, len(values), "the table names values the test does not list")
	for number, name := range names {
		if number != 0 {
			assert.True(t, reached[number], "%s is not reachable", name)
		}
	}
}

// AC-641: the tables cover every application constant and every GraphQL value.
func TestEnumTablesAreComplete(t *testing.T) {
	assertComplete(t, orderStatuses, []tracking.Status{
		tracking.StatusReceived, tracking.StatusPaid, tracking.StatusConfirmed, tracking.StatusPreparing, tracking.StatusReadyForPickup,
		tracking.StatusPickedUp, tracking.StatusDelivered, tracking.StatusCancelled, tracking.StatusFailed,
	}, service.OrderStatus_name)
	assertComplete(t, orderEndReasons, []tracking.EndReason{
		tracking.EndCustomerCancelled, tracking.EndRestaurantRejected, tracking.EndDeliveryFailed, tracking.EndPaymentDeclined, tracking.EndNotFulfilled,
	}, service.OrderEndReason_name)
	assertComplete(t, orderMilestoneKinds, []tracking.MilestoneKind{
		tracking.MilestonePlaced, tracking.MilestonePaid, tracking.MilestoneConfirmed, tracking.MilestonePreparationStarted, tracking.MilestoneReadyForPickup,
		tracking.MilestoneCourierAssigned, tracking.MilestonePickedUp, tracking.MilestoneDelivered, tracking.MilestoneEnded,
	}, service.OrderMilestoneKind_name)
	assertComplete(t, cancellationOutcomes, []tracking.CancellationOutcome{
		tracking.OutcomeRequested, tracking.OutcomeAlreadyCancelled,
	}, service.OrderCancellationOutcome_name)
	assertComplete(t, cancellationProblems, []tracking.CancellationProblem{
		tracking.ProblemWindowNotOpen, tracking.ProblemWindowExpired, tracking.ProblemOrderAlreadyFinal,
	}, service.OrderCancellationProblemCode_name)
	assertComplete(t, placeOrderProblemCodes, []checkoutapp.ProblemCode{
		checkoutapp.ProblemOrderItemUnavailable, checkoutapp.ProblemOrderTotalBelowMinimum,
		checkoutapp.ProblemOrderTotalAboveMaximum, checkoutapp.ProblemRestaurantNotAcceptingOrders,
	}, service.PlaceOrderProblemCode_name)
}

// AC-641: an unknown value is an error, never UNSPECIFIED.
func TestUnknownValuesAreErrors(t *testing.T) {
	valid := tracking.OrderView{ID: "order-1", Status: tracking.StatusCancelled}
	_, err := orderMessage(valid)
	require.NoError(t, err)

	for name, view := range map[string]tracking.OrderView{
		"status":     {Status: "LOST"},
		"end reason": {Status: tracking.StatusCancelled, EndReason: "LOST"},
		"milestone":  {Status: tracking.StatusCancelled, Milestones: []tracking.Milestone{{Kind: "LOST"}}},
	} {
		_, err := orderMessage(view)
		assert.Error(t, err, name)
	}

	_, err = cancellationPayload(tracking.CancellationResult{Problem: "LOST"})
	assert.Error(t, err, "cancellation problem")
	_, err = cancellationPayload(tracking.CancellationResult{Outcome: "LOST"})
	assert.Error(t, err, "cancellation outcome")
	_, err = placeOrderPayload(checkoutapp.Result{Problems: []checkoutapp.Problem{{Code: "LOST"}}})
	assert.Error(t, err, "place order problem")
	_, err = placeOrderPayload(checkoutapp.Result{Order: &tracking.OrderView{Status: "LOST"}})
	assert.Error(t, err, "placed order")
}

// AC-884: Order.tracking carries the channel, the Virtual City, the points
// and the courier position in RFC 3339.
func TestTrackingMessage(t *testing.T) {
	message := trackingMessage(tracking.Tracking{
		Channel: "cd",
		City: tracking.VirtualCity{
			Origin:      tracking.GeoPoint{Latitude: 52.515, Longitude: 13.385},
			Blocks:      12,
			BlockMeters: 150,
		},
		Pickup:  tracking.GeoPoint{Latitude: 52.52, Longitude: 13.39},
		Dropoff: tracking.GeoPoint{Latitude: 52.525, Longitude: 13.40},
		CourierPosition: &tracking.CourierPosition{
			Channel:    "cd",
			Point:      tracking.GeoPoint{Latitude: 52.521, Longitude: 13.392},
			RecordedAt: time.Date(2026, 10, 27, 11, 20, 0, 0, time.UTC),
		},
	})

	assert.Equal(t, "cd", message.GetChannel())
	assert.Equal(t, int32(12), message.GetCity().GetBlocks())
	assert.InDelta(t, 52.515, message.GetCity().GetOrigin().GetLatitude(), 1e-12)
	assert.InDelta(t, 13.40, message.GetDropoff().GetLongitude(), 1e-12)
	assert.Equal(t, "cd", message.GetCourierPosition().GetChannel())
	assert.Equal(t, "2026-10-27T11:20:00Z", message.GetCourierPosition().GetRecordedAt())

	without := trackingMessage(tracking.Tracking{Channel: "cd"})
	assert.Nil(t, without.GetCourierPosition())
}
