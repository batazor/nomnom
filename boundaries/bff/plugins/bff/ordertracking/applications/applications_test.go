package applications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var placedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return placedAt.Add(time.Duration(minutes) * time.Minute) }

// orderReading expects one read of order.
func orderReading(t *testing.T, order OrderSnapshot) *MockOrderReader {
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, order.ID).Return(order, nil).Once()
	return orders
}

// catalogReading expects exactly one read of each restaurant.
func catalogReading(t *testing.T, restaurantIDs ...string) *MockCatalog {
	catalog := NewMockCatalog(t)
	for _, id := range restaurantIDs {
		catalog.EXPECT().Restaurant(mock.Anything, id).Return(Restaurant{ID: id, Name: "Кухня №8", IsOpen: true}, nil).Once()
	}
	return catalog
}

func snapshot(state string, facts ...Fact) OrderSnapshot {
	return OrderSnapshot{
		ID: "order-1", RestaurantID: "restaurant-1", State: state,
		Items:    []OrderLine{{MenuItemID: "menu-tea", Name: "Чай", Quantity: 3, UnitPrice: Money{Amount: "0.10", Currency: "EUR"}}},
		Total:    Money{Amount: "0.30", Currency: "EUR"},
		PlacedAt: placedAt, Facts: facts,
	}
}

func view(t *testing.T, order OrderSnapshot, now time.Time) OrderView {
	t.Helper()
	// The courier rules are TestCourierIsNamedOnceAssigned's: here delivery
	// names one whenever it is asked.
	couriers := NewMockCouriers(t)
	couriers.EXPECT().CourierOf(mock.Anything, order.ID).Return(&Courier{Name: "Тимур"}, nil).Maybe()
	h := NewGetOrder(orderReading(t, order), catalogReading(t, order.RestaurantID), couriers)
	h.now = func() time.Time { return now }
	result, err := h.Handle(context.Background(), order.ID)
	require.NoError(t, err)
	return result
}

func TestStatusFollowsStateAndFacts(t *testing.T) {
	cancelled := func(outcome string) OrderSnapshot {
		order := snapshot("cancelled")
		order.Cancellation = &Cancellation{Outcome: outcome, ReasonCode: "customer_requested", CancelledAt: at(2)}
		return order
	}
	tests := []struct {
		name  string
		order OrderSnapshot
		want  Status
	}{
		{"placed", snapshot("placed"), StatusReceived},
		// AC-831: the recorded payment before acceptance is PAID.
		{"paid", snapshot("placed", Fact{"payment_authorized", at(1)}), StatusPaid},
		{"confirmed", snapshot("confirmed"), StatusConfirmed},
		{"preparing", snapshot("confirmed", Fact{"preparation_started", at(3)}), StatusPreparing},
		{"ready", snapshot("confirmed", Fact{"preparation_started", at(3)}, Fact{"ready_for_pickup", at(9)}), StatusReadyForPickup},
		{"picked up", snapshot("confirmed", Fact{"ready_for_pickup", at(9)}, Fact{"picked_up", at(12)}), StatusPickedUp},
		{"completed", snapshot("completed"), StatusDelivered},
		{"cancelled", cancelled("cancellation"), StatusCancelled},
		{"failed", cancelled("failure"), StatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, view(t, tt.order, at(1)).Status)
		})
	}
}

func TestUnknownStateIsAnError(t *testing.T) {
	h := NewGetOrder(orderReading(t, snapshot("lost")), catalogReading(t, "restaurant-1"), NewMockCouriers(t))
	_, err := h.Handle(context.Background(), "order-1")
	assert.Error(t, err)
}

func TestEndReasonByReasonCode(t *testing.T) {
	for code, want := range map[string]EndReason{
		"customer_requested":            EndCustomerCancelled,
		"restaurant_acceptance_timeout": EndRestaurantRejected,
		"pickup_timeout":                EndDeliveryFailed,
		// AC-832: a declined Authorization or Capture is PAYMENT_DECLINED.
		"payment_declined":         EndPaymentDeclined,
		"payment_capture_declined": EndPaymentDeclined,
		"preparation_timeout":      EndNotFulfilled,
	} {
		order := snapshot("cancelled")
		order.Cancellation = &Cancellation{Outcome: "failure", ReasonCode: code, CancelledAt: at(5)}
		assert.Equal(t, want, view(t, order, at(6)).EndReason, code)
	}
	assert.Empty(t, view(t, snapshot("placed"), at(1)).EndReason)
}

// AC-831: the payment moment is the PAID milestone after PLACED.
func TestPaymentIsPaidMilestone(t *testing.T) {
	order := snapshot("placed", Fact{"payment_authorized", at(1)})

	got := view(t, order, at(2))

	assert.Equal(t, []Milestone{{MilestonePlaced, placedAt}, {MilestonePaid, at(1)}}, got.Milestones)
}

// AC-564: the acceptance moment becomes CONFIRMED between PLACED and later facts.
func TestRestaurantAcceptanceIsConfirmedMilestone(t *testing.T) {
	order := snapshot("confirmed", Fact{"preparation_started", at(4)}, Fact{"restaurant_accepted", at(3)})

	got := view(t, order, at(5))

	assert.Equal(t, []Milestone{
		{MilestonePlaced, placedAt},
		{MilestoneConfirmed, at(3)},
		{MilestonePreparationStarted, at(4)},
	}, got.Milestones)
}

// AC-557: preparing with a courier assigned; milestones in time order.
func TestPreparingOrderWithCourierMilestones(t *testing.T) {
	order := snapshot("confirmed", Fact{"courier_assigned", at(8)}, Fact{"preparation_started", at(3)})

	got := view(t, order, at(9))

	assert.Equal(t, StatusPreparing, got.Status)
	assert.Equal(t, []Milestone{
		{MilestonePlaced, placedAt},
		{MilestonePreparationStarted, at(3)},
		{MilestoneCourierAssigned, at(8)},
	}, got.Milestones)
}

// AC-795: an Order with an Assignment names its courier; before it delivery
// is not asked.
func TestCourierIsNamedOnceAssigned(t *testing.T) {
	assigned := snapshot("confirmed", Fact{"courier_assigned", at(8)})
	couriers := NewMockCouriers(t)
	couriers.EXPECT().CourierOf(mock.Anything, assigned.ID).Return(&Courier{Name: "Тимур"}, nil).Once()
	h := NewGetOrder(orderReading(t, assigned), catalogReading(t, "restaurant-1"), couriers)

	got, err := h.Handle(context.Background(), assigned.ID)

	require.NoError(t, err)
	assert.Equal(t, &Courier{Name: "Тимур"}, got.Courier)

	// Delivery is not asked: the couriers expect no call.
	waiting := snapshot("confirmed")
	got, err = NewGetOrder(orderReading(t, waiting), catalogReading(t, "restaurant-1"), NewMockCouriers(t)).Handle(context.Background(), waiting.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Courier)

	down := errors.New("delivery down")
	failing := NewMockCouriers(t)
	failing.EXPECT().CourierOf(mock.Anything, assigned.ID).Return(nil, down).Once()
	_, err = NewGetOrder(orderReading(t, assigned), catalogReading(t, "restaurant-1"), failing).Handle(context.Background(), assigned.ID)
	assert.ErrorIs(t, err, down)
}

// AC-558: the window is offered only while it is open and the Order is active.
// FR-611: the view carries the deadlines orders named, unnamed ones stay nil.
func TestViewCarriesDeadlines(t *testing.T) {
	ready, pickup := at(12), at(15)
	order := snapshot("confirmed", Fact{Kind: "restaurant_accepted", OccurredAt: at(1)})
	order.Deadlines = Deadlines{ExpectedReadyAt: &ready, ExpectedPickupAt: &pickup}

	got := view(t, order, at(2))

	assert.Equal(t, Deadlines{ExpectedReadyAt: &ready, ExpectedPickupAt: &pickup}, got.Deadlines)
}

func TestCancellationAvailability(t *testing.T) {
	order := snapshot("placed")
	order.Window = &CancellationWindow{OpensAt: placedAt, ClosesAt: at(5)}

	assert.Equal(t, at(5), *view(t, order, at(2)).AvailableUntil)
	assert.Nil(t, view(t, order, at(6)).AvailableUntil, "after closes_at")

	delivered := snapshot("completed")
	delivered.Window = order.Window
	assert.Nil(t, view(t, delivered, at(2)).AvailableUntil, "final order")
}

// AC-559: line totals are exact decimals with the scale of the unit price.
func TestLineTotalIsExact(t *testing.T) {
	got := view(t, snapshot("placed"), at(1)).Items

	require.Len(t, got, 1)
	assert.Equal(t, "Чай", got[0].Name)
	assert.Equal(t, Money{Amount: "0.30", Currency: "EUR"}, got[0].LineTotal)

	total, err := lineTotal(Money{Amount: "690", Currency: "RUB"}, 2)
	require.NoError(t, err)
	assert.Equal(t, "1380", total.Amount)
}

// The name is the one orders kept at Place Order: an Order placed before
// names were kept shows an empty name, not the current menu.
func TestItemNameComesFromTheOrder(t *testing.T) {
	order := snapshot("placed")
	order.Items[0].Name = ""
	h := NewGetOrder(orderReading(t, order), catalogReading(t, "restaurant-1"), NewMockCouriers(t))
	got, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Empty(t, got.Items[0].Name)
}

// AC-560: business refusals become problems, other errors stay errors.
func TestCancellationRefusalsBecomeProblems(t *testing.T) {
	request := CancellationRequest{OrderID: "order-1", RequestID: "r-1"}
	for reason, want := range map[string]CancellationProblem{
		"CANCELLATION_WINDOW_NOT_OPEN": ProblemWindowNotOpen,
		"CANCELLATION_WINDOW_EXPIRED":  ProblemWindowExpired,
		"ORDER_STATE_CONFLICT":         ProblemOrderAlreadyFinal,
	} {
		h := NewRequestCancellation(cancelling(t, request, "", CancellationRefused{Reason: reason}))
		got, err := h.Handle(context.Background(), request)
		require.NoError(t, err, reason)
		assert.Equal(t, CancellationResult{Problem: want}, got, reason)
	}

	got, err := NewRequestCancellation(cancelling(t, request, OutcomeRequested, nil)).Handle(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, CancellationResult{Outcome: OutcomeRequested}, got)

	unavailable := errors.New("unavailable")
	_, err = NewRequestCancellation(cancelling(t, CancellationRequest{}, "", unavailable)).Handle(context.Background(), CancellationRequest{})
	assert.ErrorIs(t, err, unavailable)
}

// cancelling expects one Cancellation Request answered with outcome and err.
func cancelling(t *testing.T, request CancellationRequest, outcome CancellationOutcome, err error) *MockCancellationRequester {
	orders := NewMockCancellationRequester(t)
	orders.EXPECT().RequestCancellation(mock.Anything, request).Return(outcome, err).Once()
	return orders
}
