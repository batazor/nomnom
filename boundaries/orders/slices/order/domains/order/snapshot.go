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
	PlacedAt           time.Time
	State              State
	Version            uint64
	Facts              []RecordedFact
	CancellationReason CancellationReason
	CancelledAt        time.Time
	Expected           ExpectedTimes
}

func (o *Order) Snapshot() Snapshot {
	return Snapshot{
		ID:                 o.id,
		CustomerID:         o.customerID,
		RestaurantID:       o.restaurantID,
		MarketID:           o.marketID,
		Items:              o.Items(),
		PlacedAt:           o.placedAt,
		State:              o.state,
		Version:            o.version,
		Facts:              o.Facts(),
		CancellationReason: o.cancellation,
		CancelledAt:        o.cancelledAt,
		Expected:           o.expected,
	}
}

// Restore rebuilds a persisted Order without recording domain events.
func Restore(snapshot Snapshot) (*Order, error) {
	if !isKnownState(snapshot.State) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownOrderState, snapshot.State)
	}
	order, err := New(snapshot.ID, snapshot.CustomerID, snapshot.RestaurantID, snapshot.MarketID, snapshot.Items)
	if err != nil {
		return nil, err
	}
	seen := make(map[FulfillmentFact]struct{}, len(snapshot.Facts))
	for _, fact := range snapshot.Facts {
		if _, duplicate := seen[fact.Fact]; duplicate || !isKnownFact(fact.Fact) {
			return nil, fmt.Errorf("%w: fact %q", ErrInvalidSnapshot, fact.Fact)
		}
		seen[fact.Fact] = struct{}{}
	}
	order.placedAt = snapshot.PlacedAt
	order.state = snapshot.State
	order.version = snapshot.Version
	order.facts = append([]RecordedFact(nil), snapshot.Facts...)
	order.cancellation = snapshot.CancellationReason
	order.cancelledAt = snapshot.CancelledAt
	order.expected = snapshot.Expected
	return order, nil
}

func isKnownState(state State) bool {
	for _, candidate := range allStates {
		if candidate == state {
			return true
		}
	}
	return false
}
