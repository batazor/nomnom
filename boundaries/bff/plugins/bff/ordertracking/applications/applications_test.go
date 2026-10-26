package applications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var placedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return placedAt.Add(time.Duration(minutes) * time.Minute) }

type fakeOrders struct {
	order   OrderSnapshot
	err     error
	outcome CancellationOutcome
	got     CancellationRequest
}

func (f *fakeOrders) GetOrder(context.Context, string) (OrderSnapshot, error) { return f.order, f.err }

func (f *fakeOrders) RequestCancellation(_ context.Context, request CancellationRequest) (CancellationOutcome, error) {
	f.got = request
	return f.outcome, f.err
}

// fakeCatalog counts the restaurant reads; a restaurant in err fails.
// fakeCouriers names the courier of every Order it is asked about.
type fakeCouriers struct {
	asked *[]string
	err   error
}

func (f fakeCouriers) CourierOf(_ context.Context, orderID string) (*Courier, error) {
	if f.asked != nil {
		*f.asked = append(*f.asked, orderID)
	}
	if f.err != nil {
		return nil, f.err
	}
	return &Courier{Name: "Тимур"}, nil
}

type fakeCatalog struct {
	reads map[string]int
	err   map[string]error
}

func (f fakeCatalog) Restaurant(_ context.Context, id string) (Restaurant, error) {
	if f.reads != nil {
		f.reads[id]++
	}
	if err := f.err[id]; err != nil {
		return Restaurant{}, err
	}
	return Restaurant{ID: id, Name: "Кухня №8", IsOpen: true}, nil
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
	h := NewGetOrder(&fakeOrders{order: order}, fakeCatalog{}, fakeCouriers{})
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
	h := NewGetOrder(&fakeOrders{order: snapshot("lost")}, fakeCatalog{}, fakeCouriers{})
	_, err := h.Handle(context.Background(), "order-1")
	assert.Error(t, err)
}

func TestEndReasonByReasonCode(t *testing.T) {
	for code, want := range map[string]EndReason{
		"customer_requested":            EndCustomerCancelled,
		"restaurant_acceptance_timeout": EndRestaurantRejected,
		"pickup_timeout":                EndDeliveryFailed,
		"payment_declined":              EndNotFulfilled,
	} {
		order := snapshot("cancelled")
		order.Cancellation = &Cancellation{Outcome: "failure", ReasonCode: code, CancelledAt: at(5)}
		assert.Equal(t, want, view(t, order, at(6)).EndReason, code)
	}
	assert.Empty(t, view(t, snapshot("placed"), at(1)).EndReason)
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
	var asked []string
	assigned := snapshot("confirmed", Fact{"courier_assigned", at(8)})
	h := NewGetOrder(&fakeOrders{order: assigned}, fakeCatalog{}, fakeCouriers{asked: &asked})

	got, err := h.Handle(context.Background(), assigned.ID)

	require.NoError(t, err)
	assert.Equal(t, &Courier{Name: "Тимур"}, got.Courier)
	assert.Equal(t, []string{assigned.ID}, asked)

	asked = nil
	waiting := snapshot("confirmed")
	got, err = NewGetOrder(&fakeOrders{order: waiting}, fakeCatalog{}, fakeCouriers{asked: &asked}).Handle(context.Background(), waiting.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Courier)
	assert.Empty(t, asked)

	down := errors.New("delivery down")
	_, err = NewGetOrder(&fakeOrders{order: assigned}, fakeCatalog{}, fakeCouriers{err: down}).Handle(context.Background(), assigned.ID)
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
	h := NewGetOrder(&fakeOrders{order: order}, fakeCatalog{}, fakeCouriers{})
	got, err := h.Handle(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Empty(t, got.Items[0].Name)
}

// AC-560: business refusals become problems, other errors stay errors.
func TestCancellationRefusalsBecomeProblems(t *testing.T) {
	for reason, want := range map[string]CancellationProblem{
		"CANCELLATION_WINDOW_NOT_OPEN": ProblemWindowNotOpen,
		"CANCELLATION_WINDOW_EXPIRED":  ProblemWindowExpired,
		"ORDER_STATE_CONFLICT":         ProblemOrderAlreadyFinal,
	} {
		h := NewRequestCancellation(&fakeOrders{err: CancellationRefused{Reason: reason}})
		got, err := h.Handle(context.Background(), CancellationRequest{OrderID: "order-1", RequestID: "r-1"})
		require.NoError(t, err, reason)
		assert.Equal(t, CancellationResult{Problem: want}, got, reason)
	}

	orders := &fakeOrders{outcome: OutcomeRequested}
	got, err := NewRequestCancellation(orders).Handle(context.Background(), CancellationRequest{OrderID: "order-1", RequestID: "r-1"})
	require.NoError(t, err)
	assert.Equal(t, CancellationResult{Outcome: OutcomeRequested}, got)
	assert.Equal(t, "r-1", orders.got.RequestID)

	unavailable := errors.New("unavailable")
	_, err = NewRequestCancellation(&fakeOrders{err: unavailable}).Handle(context.Background(), CancellationRequest{})
	assert.ErrorIs(t, err, unavailable)
}
