package applications

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
)

// Status is the customer-facing lifecycle of an Order (OrderStatus of the
// public schema).
type Status string

const (
	StatusReceived       Status = "RECEIVED"
	StatusConfirmed      Status = "CONFIRMED"
	StatusPreparing      Status = "PREPARING"
	StatusReadyForPickup Status = "READY_FOR_PICKUP"
	StatusPickedUp       Status = "PICKED_UP"
	StatusDelivered      Status = "DELIVERED"
	StatusCancelled      Status = "CANCELLED"
	StatusFailed         Status = "FAILED"
)

// Final reports whether the Order no longer changes.
func (s Status) Final() bool {
	return s == StatusDelivered || s == StatusCancelled || s == StatusFailed
}

// EndReason explains an Order that ended without delivery.
type EndReason string

const (
	EndCustomerCancelled  EndReason = "CUSTOMER_CANCELLED"
	EndRestaurantRejected EndReason = "RESTAURANT_REJECTED"
	EndDeliveryFailed     EndReason = "DELIVERY_FAILED"
	EndNotFulfilled       EndReason = "NOT_FULFILLED"
)

// MilestoneKind is a notable moment of an Order (OrderMilestoneKind).
type MilestoneKind string

const (
	MilestonePlaced             MilestoneKind = "PLACED"
	MilestoneConfirmed          MilestoneKind = "CONFIRMED"
	MilestonePreparationStarted MilestoneKind = "PREPARATION_STARTED"
	MilestoneReadyForPickup     MilestoneKind = "READY_FOR_PICKUP"
	MilestoneCourierAssigned    MilestoneKind = "COURIER_ASSIGNED"
	MilestonePickedUp           MilestoneKind = "PICKED_UP"
	MilestoneDelivered          MilestoneKind = "DELIVERED"
	MilestoneEnded              MilestoneKind = "ENDED"
)

type Milestone struct {
	Kind       MilestoneKind
	OccurredAt time.Time
}

// OrderView is the order as the customer UI shows it.
type OrderView struct {
	ID         string
	Status     Status
	EndReason  EndReason
	Restaurant Restaurant
	Items      []ItemView
	Total      Money
	PlacedAt   time.Time
	Milestones []Milestone
	// AvailableUntil is set only while the customer may request cancellation.
	AvailableUntil *time.Time
	Deadlines      Deadlines
}

type ItemView struct {
	MenuItemID string
	// Name is empty when the item is no longer in the menu.
	Name      string
	Quantity  int
	UnitPrice Money
	LineTotal Money
}

var milestoneOfFact = map[string]MilestoneKind{
	"restaurant_accepted": MilestoneConfirmed,
	"preparation_started": MilestonePreparationStarted,
	"ready_for_pickup":    MilestoneReadyForPickup,
	"courier_assigned":    MilestoneCourierAssigned,
	"picked_up":           MilestonePickedUp,
	"delivery_completed":  MilestoneDelivered,
}

var endReasonOfCode = map[string]EndReason{
	"customer_requested":            EndCustomerCancelled,
	"restaurant_rejected":           EndRestaurantRejected,
	"restaurant_acceptance_timeout": EndRestaurantRejected,
	"delivery_failed":               EndDeliveryFailed,
	"delivery_timeout":              EndDeliveryFailed,
	"pickup_timeout":                EndDeliveryFailed,
}

// status computes the customer status from the Order State and its facts;
// an unknown state is an error, not a guess.
func status(order OrderSnapshot) (Status, error) {
	has := func(kind string) bool {
		return slices.ContainsFunc(order.Facts, func(f Fact) bool { return f.Kind == kind })
	}
	switch order.State {
	case "placed":
		return StatusReceived, nil
	case "completed":
		return StatusDelivered, nil
	case "cancelled":
		if order.Cancellation != nil && order.Cancellation.Outcome == "failure" {
			return StatusFailed, nil
		}
		return StatusCancelled, nil
	case "confirmed":
		switch {
		case has("picked_up"):
			return StatusPickedUp, nil
		case has("ready_for_pickup"):
			return StatusReadyForPickup, nil
		case has("preparation_started"):
			return StatusPreparing, nil
		default:
			return StatusConfirmed, nil
		}
	}
	return "", fmt.Errorf("unknown order state %q", order.State)
}

func endReason(order OrderSnapshot) EndReason {
	if order.Cancellation == nil {
		return ""
	}
	if reason, ok := endReasonOfCode[order.Cancellation.ReasonCode]; ok {
		return reason
	}
	return EndNotFulfilled
}

// milestones lists PLACED, the facts that are milestones and ENDED by time.
// CONFIRMED comes from restaurant_accepted; Orders confirmed before orders
// recorded that fact have none.
func milestones(order OrderSnapshot) []Milestone {
	result := []Milestone{{Kind: MilestonePlaced, OccurredAt: order.PlacedAt}}
	for _, fact := range order.Facts {
		if kind, ok := milestoneOfFact[fact.Kind]; ok {
			result = append(result, Milestone{Kind: kind, OccurredAt: fact.OccurredAt})
		}
	}
	if order.Cancellation != nil {
		result = append(result, Milestone{Kind: MilestoneEnded, OccurredAt: order.Cancellation.CancelledAt})
	}
	slices.SortStableFunc(result, func(a, b Milestone) int { return a.OccurredAt.Compare(b.OccurredAt) })
	return result
}

// availableUntil is the end of the cancellation window when it is open now.
func availableUntil(order OrderSnapshot, current Status, now time.Time) *time.Time {
	window := order.Window
	if window == nil || current.Final() || now.Before(window.OpensAt) || now.After(window.ClosesAt) {
		return nil
	}
	until := window.ClosesAt
	return &until
}

// lineTotal multiplies exactly, keeping the scale of the unit price.
func lineTotal(price Money, quantity int) (Money, error) {
	unit, ok := new(big.Rat).SetString(price.Amount)
	if !ok {
		return Money{}, fmt.Errorf("invalid amount %q", price.Amount)
	}
	scale := 0
	if _, fraction, found := strings.Cut(price.Amount, "."); found {
		scale = len(fraction)
	}
	total := unit.Mul(unit, new(big.Rat).SetInt64(int64(quantity)))
	return Money{Amount: total.FloatString(scale), Currency: price.Currency}, nil
}
