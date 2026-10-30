package order

import "time"

// ExpectedTimes are the latest Fulfillment deadlines named by their owners
// (FR-611): restaurants names when the Order is ready, delivery — when the
// courier picks it up and hands it over. A zero time is not named. They are
// Order data reported by neighbouring contexts, not a schedule of orders: the
// process deadlines of the workflow are separate.
type ExpectedTimes struct {
	ReadyAt     time.Time
	PickupAt    time.Time
	DeliveredAt time.Time
}

// ExpectedTimes returns the latest named Fulfillment deadlines.
func (o *Order) ExpectedTimes() ExpectedTimes { return o.expected }

// ExpectReadyAt replaces the expected ready time with the one named at
// occurredAt: on Acceptance and on every Preparation delay. changed is false
// when the time is the stored one.
func (o *Order) ExpectReadyAt(occurredAt, readyAt time.Time) (bool, error) {
	return o.expect(occurredAt, &o.expected.ReadyAt, readyAt)
}

// ExpectPickupAt replaces the expected pickup time with the one of the latest
// Courier Assignment: a new courier names its own time or none.
func (o *Order) ExpectPickupAt(occurredAt, pickupAt time.Time) (bool, error) {
	return o.expect(occurredAt, &o.expected.PickupAt, pickupAt)
}

// ExpectDeliveredAt replaces the expected handover time named at Pickup.
func (o *Order) ExpectDeliveredAt(occurredAt, deliveredAt time.Time) (bool, error) {
	return o.expect(occurredAt, &o.expected.DeliveredAt, deliveredAt)
}

// expect lets the FSM admit the change like a step of Fulfillment: a final Order
// takes no new deadlines.
func (o *Order) expect(occurredAt time.Time, stored *time.Time, next time.Time) (bool, error) {
	if _, err := transition(o.state, TriggerUpdateDetails, TransitionInput{OccurredAt: occurredAt}); err != nil {
		return false, err
	}
	if stored.Equal(next) {
		return false, nil
	}
	*stored = next
	return true, nil
}
