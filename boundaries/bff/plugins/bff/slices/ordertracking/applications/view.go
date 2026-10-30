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
	StatusPaid           Status = "PAID"
	StatusScheduled      Status = "SCHEDULED"
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
	EndPaymentDeclined    EndReason = "PAYMENT_DECLINED"
	EndNotFulfilled       EndReason = "NOT_FULFILLED"
)

// MilestoneKind is a notable moment of an Order (OrderMilestoneKind).
type MilestoneKind string

const (
	MilestonePlaced             MilestoneKind = "PLACED"
	MilestonePaid               MilestoneKind = "PAID"
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
	Priority   Priority
	// PremiumFee is nil for a standard Order.
	PremiumFee *Money
	PlacedAt   time.Time
	Milestones []Milestone
	// AvailableUntil is set only while the customer may request cancellation.
	AvailableUntil *time.Time
	Deadlines      Deadlines
	// Courier is the courier delivering the Order (FR-626); nil before an
	// Assignment and in the order history, which does not read it.
	Courier *Courier
	// Tracking is the live tracking of the courier (FR-663); nil without an
	// assigned or picked up delivery and in the order history.
	Tracking *Tracking
	// DeliveryWindow is set only for a scheduled Order.
	DeliveryWindow *DeliveryWindow
}

// Tracking is the live tracking of the delivery of an Order: the Tracking
// Channel of the courierMoved subscription, the Virtual City and its points
// and the stored courier position.
type Tracking struct {
	Channel string
	City    VirtualCity
	Pickup  GeoPoint
	Dropoff GeoPoint
	// CourierPosition is nil while delivery knows no position.
	CourierPosition *CourierPosition
}

// VirtualCity is a grid of Blocks × Blocks blocks of BlockMeters from the
// south-west corner Origin.
type VirtualCity struct {
	Origin      GeoPoint
	Blocks      int
	BlockMeters float64
}

type GeoPoint struct {
	Latitude  float64
	Longitude float64
}

// CourierPosition is a courier position with the moment delivery recorded it.
type CourierPosition struct {
	Channel    string
	Point      GeoPoint
	RecordedAt time.Time
}

// Courier names the courier of an Order for its customer; the cook stays
// unnamed.
type Courier struct {
	Name string
}

type ItemView struct {
	MenuItemID string
	// Name is the name at Place Order; empty for Orders placed before orders kept it.
	Name      string
	Quantity  int
	UnitPrice Money
	LineTotal Money
}

var milestoneOfTrigger = map[string]MilestoneKind{
	"authorize_payment": MilestonePaid,
	"confirm":           MilestoneConfirmed,
	"start_preparation": MilestonePreparationStarted,
	"mark_ready":        MilestoneReadyForPickup,
	"pick_up":           MilestonePickedUp,
	"deliver":           MilestoneDelivered,
}

var endReasonOfCode = map[string]EndReason{
	"customer_requested":            EndCustomerCancelled,
	"restaurant_rejected":           EndRestaurantRejected,
	"restaurant_acceptance_timeout": EndRestaurantRejected,
	"delivery_failed":               EndDeliveryFailed,
	"delivery_timeout":              EndDeliveryFailed,
	"pickup_timeout":                EndDeliveryFailed,
	"payment_declined":              EndPaymentDeclined,
	"payment_capture_declined":      EndPaymentDeclined,
}

// status computes the customer status from the Order State; an unknown state
// is an error, not a guess.
func status(order OrderSnapshot) (Status, error) {
	switch order.State {
	case "placed":
		return StatusReceived, nil
	case "paid":
		return StatusPaid, nil
	case "delivered", "completed":
		return StatusDelivered, nil
	case "rejected", "delivery_failed":
		return StatusFailed, nil
	case "cancelled":
		if order.Cancellation != nil && order.Cancellation.Outcome == "failure" {
			return StatusFailed, nil
		}
		return StatusCancelled, nil
	case "confirmed":
		// A confirmed scheduled Order waits for the start the plan gave it
		// (FR-685): the kitchen starts it for its window, not now.
		if order.DeliveryWindow != nil {
			return StatusScheduled, nil
		}
		return StatusConfirmed, nil
	case "preparing":
		return StatusPreparing, nil
	case "ready":
		return StatusReadyForPickup, nil
	case "picked_up":
		return StatusPickedUp, nil
	}
	return "", fmt.Errorf("unknown order state %q", order.State)
}

func endReason(order OrderSnapshot) EndReason {
	if order.Cancellation == nil {
		// The Saga has not named the reason yet; the outcome is already known.
		switch order.State {
		case "rejected":
			return EndRestaurantRejected
		case "delivery_failed":
			return EndDeliveryFailed
		}
		return ""
	}
	if reason, ok := endReasonOfCode[order.Cancellation.ReasonCode]; ok {
		return reason
	}
	return EndNotFulfilled
}

// milestones lists PLACED, the transitions that are milestones, the current
// Courier Assignment and ENDED by time. CONFIRMED comes from the confirm
// transition; Orders confirmed before orders kept its time have none.
func milestones(order OrderSnapshot) []Milestone {
	result := []Milestone{{Kind: MilestonePlaced, OccurredAt: order.PlacedAt}}
	for _, applied := range order.Transitions {
		if kind, ok := milestoneOfTrigger[applied.Trigger]; ok {
			result = append(result, Milestone{Kind: kind, OccurredAt: applied.OccurredAt})
		}
	}
	if !order.CourierAssignedAt.IsZero() {
		result = append(result, Milestone{Kind: MilestoneCourierAssigned, OccurredAt: order.CourierAssignedAt})
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
