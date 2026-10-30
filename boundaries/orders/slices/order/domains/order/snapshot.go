package order

import (
	"fmt"
	"time"

	"nomnom/orders/slices/order/domains/order/vo"
)

// Snapshot is the complete persisted form of an Order used by repositories.
type Snapshot struct {
	ID                 ID
	CustomerID         CustomerID
	RestaurantID       RestaurantID
	MarketID           vo.MarketID
	Items              []Item
	Service            Service
	PlacedAt           time.Time
	State              State
	Version            uint64
	Transitions        []AppliedTransition
	CancellationReason CancellationReason
	CancelledAt        time.Time
	Expected           ExpectedTimes
	DeliveryWindow     DeliveryWindow
}

func (o *Order) Snapshot() Snapshot {
	return Snapshot{
		ID:                 o.id,
		CustomerID:         o.customerID,
		RestaurantID:       o.restaurantID,
		MarketID:           o.marketID,
		Items:              o.Items(),
		Service:            o.service,
		PlacedAt:           o.placedAt,
		State:              o.state,
		Version:            o.version,
		Transitions:        o.Transitions(),
		CancellationReason: o.cancellation,
		CancelledAt:        o.cancelledAt,
		Expected:           o.expected,
		DeliveryWindow:     o.deliveryWindow,
	}
}

// Restore rebuilds a persisted Order without recording domain events.
func Restore(snapshot Snapshot) (*Order, error) {
	if !isKnownState(snapshot.State) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownOrderState, snapshot.State)
	}
	order, err := New(snapshot.ID, snapshot.CustomerID, snapshot.RestaurantID, snapshot.MarketID, snapshot.Items, snapshot.Service)
	if err != nil {
		return nil, err
	}
	seen := make(map[Trigger]struct{}, len(snapshot.Transitions))
	for _, applied := range snapshot.Transitions {
		if _, duplicate := seen[applied.Trigger]; duplicate || !isKnownTrigger(applied.Trigger) {
			return nil, fmt.Errorf("%w: transition %q", ErrInvalidSnapshot, applied.Trigger)
		}
		seen[applied.Trigger] = struct{}{}
	}
	order.placedAt = snapshot.PlacedAt
	order.state = snapshot.State
	order.version = snapshot.Version
	order.transitions = append([]AppliedTransition(nil), snapshot.Transitions...)
	order.cancellation = snapshot.CancellationReason
	order.cancelledAt = snapshot.CancelledAt
	order.expected = snapshot.Expected
	order.deliveryWindow = snapshot.DeliveryWindow
	return order, nil
}

func isKnownTrigger(trigger Trigger) bool {
	for _, candidate := range allTriggers {
		if candidate == trigger {
			return true
		}
	}
	return false
}

func isKnownState(state State) bool {
	for _, candidate := range allStates {
		if candidate == state {
			return true
		}
	}
	return false
}
