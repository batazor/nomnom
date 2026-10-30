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

func snapshot(state string, facts ...Fact) OrderSnapshot {
	return OrderSnapshot{
		ID: "order-1", RestaurantID: "restaurant-1", State: state,
		Items:    []OrderLine{{MenuItemID: "menu-tea", Name: "Чай", Quantity: 3, UnitPrice: Money{Amount: "0.10", Currency: "EUR"}}},
		Total:    Money{Amount: "0.30", Currency: "EUR"},
		PlacedAt: placedAt, Facts: facts,
	}
}

func TestStatusFollowsState(t *testing.T) {
	scheduled := func(state string, facts ...Fact) OrderSnapshot {
		order := snapshot(state, facts...)
		order.DeliveryWindow = &DeliveryWindow{Start: at(120), End: at(135)}
		return order
	}
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
		{"paid", snapshot("paid", Fact{"payment_authorized", at(1)}), StatusPaid},
		{"confirmed", snapshot("confirmed"), StatusConfirmed},
		{"preparing", snapshot("preparing", Fact{"preparation_started", at(3)}), StatusPreparing},
		// AC-927: a confirmed scheduled Order without Preparation Started.
		{"scheduled", scheduled("confirmed", Fact{"restaurant_accepted", at(2)}), StatusScheduled},
		{"scheduled preparing", scheduled("preparing", Fact{"preparation_started", at(100)}), StatusPreparing},
		{"scheduled paid", scheduled("paid", Fact{"payment_authorized", at(1)}), StatusPaid},
		{"ready", snapshot("ready", Fact{"preparation_started", at(3)}, Fact{"ready_for_pickup", at(9)}), StatusReadyForPickup},
		{"picked up", snapshot("picked_up", Fact{"ready_for_pickup", at(9)}, Fact{"picked_up", at(12)}), StatusPickedUp},
		{"completed", snapshot("completed"), StatusDelivered},
		{"cancelled", cancelled("cancellation"), StatusCancelled},
		{"failed", cancelled("failure"), StatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orders := NewMockOrderReader(t)
			orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(tt.order, nil).Once()
			catalog := NewMockCatalog(t)
			catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
			h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
			h.now = func() time.Time { return at(1) }
			got, err := h.Handle(context.Background(), "order-1")
			require.NoError(t, err)

			assert.Equal(t, tt.want, got.Status)
		})
	}
}

func TestUnknownStateIsAnError(t *testing.T) {
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(snapshot("lost"), nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
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
		orders := NewMockOrderReader(t)
		orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(order, nil).Once()
		catalog := NewMockCatalog(t)
		catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
		h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
		h.now = func() time.Time { return at(6) }
		got, err := h.Handle(context.Background(), "order-1")
		require.NoError(t, err)
		assert.Equal(t, want, got.EndReason, code)
	}

	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(snapshot("placed"), nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
	h.now = func() time.Time { return at(1) }
	placed, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Empty(t, placed.EndReason)
}

// AC-831: the payment moment is the PAID milestone after PLACED.
func TestPaymentIsPaidMilestone(t *testing.T) {
	order := snapshot("placed", Fact{"payment_authorized", at(1)})
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(order, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
	h.now = func() time.Time { return at(2) }
	got, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)

	assert.Equal(t, []Milestone{{MilestonePlaced, placedAt}, {MilestonePaid, at(1)}}, got.Milestones)
}

// AC-564: the acceptance moment becomes CONFIRMED between PLACED and later facts.
func TestRestaurantAcceptanceIsConfirmedMilestone(t *testing.T) {
	order := snapshot("confirmed", Fact{"preparation_started", at(4)}, Fact{"restaurant_accepted", at(3)})
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(order, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
	h.now = func() time.Time { return at(5) }
	got, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)

	assert.Equal(t, []Milestone{
		{MilestonePlaced, placedAt},
		{MilestoneConfirmed, at(3)},
		{MilestonePreparationStarted, at(4)},
	}, got.Milestones)
}

// AC-557: preparing with a courier assigned; milestones in time order.
func TestPreparingOrderWithCourierMilestones(t *testing.T) {
	order := snapshot("preparing", Fact{"courier_assigned", at(8)}, Fact{"preparation_started", at(3)})
	deliveries := NewMockDeliveries(t)
	deliveries.EXPECT().DeliveryOf(mock.Anything, "order-1").Return(OrderDelivery{Courier: &Courier{Name: "Тимур"}}, nil).Once()
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(order, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, deliveries)
	h.now = func() time.Time { return at(9) }
	got, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)

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
	deliveries := NewMockDeliveries(t)
	deliveries.EXPECT().DeliveryOf(mock.Anything, assigned.ID).Return(OrderDelivery{Courier: &Courier{Name: "Тимур"}}, nil).Once()
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(assigned, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, deliveries)

	got, err := h.Handle(context.Background(), assigned.ID)

	require.NoError(t, err)
	assert.Equal(t, &Courier{Name: "Тимур"}, got.Courier)

	// Delivery is not asked: the deliveries expect no call.
	waiting := snapshot("confirmed")
	waitingOrders := NewMockOrderReader(t)
	waitingOrders.EXPECT().GetOrder(mock.Anything, "order-1").Return(waiting, nil).Once()
	waitingCatalog := NewMockCatalog(t)
	waitingCatalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	got, err = NewGetOrder(waitingOrders, waitingCatalog, NewMockDeliveries(t)).Handle(context.Background(), waiting.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Courier)

	down := errors.New("delivery down")
	failing := NewMockDeliveries(t)
	failing.EXPECT().DeliveryOf(mock.Anything, assigned.ID).Return(OrderDelivery{}, down).Once()
	failingOrders := NewMockOrderReader(t)
	failingOrders.EXPECT().GetOrder(mock.Anything, "order-1").Return(assigned, nil).Once()
	failingCatalog := NewMockCatalog(t)
	failingCatalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	_, err = NewGetOrder(failingOrders, failingCatalog, failing).Handle(context.Background(), assigned.ID)
	assert.ErrorIs(t, err, down)
}

// AC-884, AC-886: an Order with an Assignment carries the courier and the
// tracking delivery returns in the same call; delivery returns no tracking
// for a delivered Order.
func TestTrackingOfActiveAssignedOrder(t *testing.T) {
	pickedUp := snapshot("confirmed", Fact{"courier_assigned", at(8)}, Fact{"picked_up", at(9)})
	tracking := &Tracking{
		Channel: "cd",
		City:    VirtualCity{Origin: GeoPoint{Latitude: 52.515, Longitude: 13.385}, Blocks: 12, BlockMeters: 150},
		Pickup:  GeoPoint{Latitude: 52.52, Longitude: 13.39},
		Dropoff: GeoPoint{Latitude: 52.525, Longitude: 13.40},
		CourierPosition: &CourierPosition{
			Channel:    "cd",
			Point:      GeoPoint{Latitude: 52.521, Longitude: 13.392},
			RecordedAt: at(10),
		},
	}
	deliveries := NewMockDeliveries(t)
	deliveries.EXPECT().DeliveryOf(mock.Anything, pickedUp.ID).Return(OrderDelivery{Courier: &Courier{Name: "Тимур"}, Tracking: tracking}, nil).Once()

	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(pickedUp, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	got, err := NewGetOrder(orders, catalog, deliveries).Handle(context.Background(), pickedUp.ID)

	require.NoError(t, err)
	assert.Equal(t, tracking, got.Tracking)

	delivered := snapshot("completed", Fact{"courier_assigned", at(8)}, Fact{"delivery_completed", at(20)})
	final := NewMockDeliveries(t)
	final.EXPECT().DeliveryOf(mock.Anything, delivered.ID).Return(OrderDelivery{Courier: &Courier{Name: "Тимур"}}, nil).Once()
	deliveredOrders := NewMockOrderReader(t)
	deliveredOrders.EXPECT().GetOrder(mock.Anything, "order-1").Return(delivered, nil).Once()
	deliveredCatalog := NewMockCatalog(t)
	deliveredCatalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	got, err = NewGetOrder(deliveredOrders, deliveredCatalog, final).Handle(context.Background(), delivered.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Tracking, "delivery reports no tracking for a delivered Order")
}

// AC-558: the window is offered only while it is open and the Order is active.
// FR-611: the view carries the deadlines orders named, unnamed ones stay nil.
func TestViewCarriesDeadlines(t *testing.T) {
	ready, pickup := at(12), at(15)
	order := snapshot("confirmed", Fact{Kind: "restaurant_accepted", OccurredAt: at(1)})
	order.Deadlines = Deadlines{ExpectedReadyAt: &ready, ExpectedPickupAt: &pickup}
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(order, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
	h.now = func() time.Time { return at(2) }
	got, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)

	assert.Equal(t, Deadlines{ExpectedReadyAt: &ready, ExpectedPickupAt: &pickup}, got.Deadlines)
}

func TestCancellationAvailability(t *testing.T) {
	order := snapshot("placed")
	order.Window = &CancellationWindow{OpensAt: placedAt, ClosesAt: at(5)}

	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(order, nil).Twice()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Twice()
	h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
	h.now = func() time.Time { return at(2) }
	open, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Equal(t, at(5), *open.AvailableUntil)
	h.now = func() time.Time { return at(6) }
	closed, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Nil(t, closed.AvailableUntil, "after closes_at")

	delivered := snapshot("completed")
	delivered.Window = order.Window
	finalOrders := NewMockOrderReader(t)
	finalOrders.EXPECT().GetOrder(mock.Anything, "order-1").Return(delivered, nil).Once()
	finalCatalog := NewMockCatalog(t)
	finalCatalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	final := NewGetOrder(finalOrders, finalCatalog, NewMockDeliveries(t))
	final.now = func() time.Time { return at(2) }
	got, err := final.Handle(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Nil(t, got.AvailableUntil, "final order")
}

// AC-559: line totals are exact decimals with the scale of the unit price.
func TestLineTotalIsExact(t *testing.T) {
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(snapshot("placed"), nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
	h.now = func() time.Time { return at(1) }
	placed, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)
	got := placed.Items

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
	orders := NewMockOrderReader(t)
	orders.EXPECT().GetOrder(mock.Anything, "order-1").Return(order, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, nil).Once()
	h := NewGetOrder(orders, catalog, NewMockDeliveries(t))
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
		orders := NewMockCancellationRequester(t)
		orders.EXPECT().RequestCancellation(mock.Anything, request).Return("", CancellationRefused{Reason: reason}).Once()
		h := NewRequestCancellation(orders)
		got, err := h.Handle(context.Background(), request)
		require.NoError(t, err, reason)
		assert.Equal(t, CancellationResult{Problem: want}, got, reason)
	}

	requested := NewMockCancellationRequester(t)
	requested.EXPECT().RequestCancellation(mock.Anything, request).Return(OutcomeRequested, nil).Once()
	got, err := NewRequestCancellation(requested).Handle(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, CancellationResult{Outcome: OutcomeRequested}, got)

	unavailable := errors.New("unavailable")
	failing := NewMockCancellationRequester(t)
	failing.EXPECT().RequestCancellation(mock.Anything, CancellationRequest{}).Return("", unavailable).Once()
	_, err = NewRequestCancellation(failing).Handle(context.Background(), CancellationRequest{})
	assert.ErrorIs(t, err, unavailable)
}
