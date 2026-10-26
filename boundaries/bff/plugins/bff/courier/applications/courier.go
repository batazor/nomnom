// Package applications holds the scenarios of the courier screen: the work of
// the current courier and the courier commands. The courier is the verified
// user: delivery registers couriers under their user id and authorizes
// courier#act itself. Protobuf and GraphQL types do not reach it (BFF ADR 2).
package applications

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotCourier is a user who is not a registered courier, or who may not
	// act as the courier.
	ErrNotCourier = errors.New("user is not a registered courier")
	// ErrDeliveryNotFound is a delivery unknown to delivery.
	ErrDeliveryNotFound = errors.New("delivery not found")
	// ErrOfferNotAvailable is an offer that expired, was taken or is unknown.
	ErrOfferNotAvailable = errors.New("delivery offer is not available")
	// ErrStageConflict is a delivery command the current stage forbids.
	ErrStageConflict = errors.New("delivery stage forbids the command")
	// ErrAvailabilityConflict is an availability change delivery refuses in
	// the current courier status, for example while the courier is busy.
	ErrAvailabilityConflict = errors.New("courier status forbids the availability change")
	// ErrInvalidCommand is a command delivery rejects as malformed.
	ErrInvalidCommand = errors.New("courier command is invalid")
	// ErrUnknownCommand is an availability or action outside the courier
	// commands.
	ErrUnknownCommand = errors.New("unknown courier command")
)

// Availability of a courier for new deliveries.
type Availability string

const (
	AvailabilityAvailable   Availability = "available"
	AvailabilityBusy        Availability = "busy"
	AvailabilityUnavailable Availability = "unavailable"
)

// DeliveryStage is the stage of a delivery assigned to the courier.
type DeliveryStage string

const (
	StageAssigned DeliveryStage = "assigned"
	StagePickedUp DeliveryStage = "picked_up"
)

// Action is a courier command over the current delivery.
type Action string

const (
	ActionConfirmPickup   Action = "confirm_pickup"
	ActionConfirmDelivery Action = "confirm_delivery"
)

// Offer is a pending delivery offer addressed to the courier.
type Offer struct {
	ID         string
	DeliveryID string
	OrderID    string
	ExpiresAt  time.Time
}

// Delivery is the delivery the courier is carrying out.
type Delivery struct {
	ID      string
	OrderID string
	Stage   DeliveryStage
}

// CourierState is the status of the courier and its current delivery.
type CourierState struct {
	Availability    Availability
	CurrentDelivery *Delivery
}

// Couriers runs the courier RPCs of delivery on behalf of the user in the
// context; courierID is that user. Every method returns ErrNotCourier for a
// user without courier#act on courierID or an unregistered courier.
type Couriers interface {
	State(ctx context.Context, courierID string) (CourierState, error)
	Offers(ctx context.Context, courierID string) ([]Offer, error)
	// SetAvailability also returns ErrAvailabilityConflict.
	SetAvailability(ctx context.Context, courierID string, availability Availability) error
	// AcceptOffer also returns ErrOfferNotAvailable.
	AcceptOffer(ctx context.Context, courierID, offerID string) error
	// ConfirmPickup and ConfirmDelivery also return ErrDeliveryNotFound and
	// ErrStageConflict.
	ConfirmPickup(ctx context.Context, courierID, deliveryID string) error
	ConfirmDelivery(ctx context.Context, courierID, deliveryID string) error
}

// CourierView is the courier screen. Offers are empty unless Available.
type CourierView struct {
	Availability    Availability
	Offers          []Offer
	CurrentDelivery *Delivery
}

// Problem is a business reason why a courier command cannot be applied.
type Problem string

const (
	ProblemOfferNotAvailable     Problem = "offer_not_available"
	ProblemDeliveryStageConflict Problem = "delivery_stage_conflict"
)

// CommandResult is the courier after a command and the problem, empty on
// success.
type CommandResult struct {
	Courier CourierView
	Problem Problem
}
