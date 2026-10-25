package applications

import (
	"context"
	"errors"
	"fmt"
)

// courierCommand runs one command and reads the courier back; a refusal named
// by problems becomes the problem of the payload, the rest stay errors.
func courierCommand(ctx context.Context, couriers Couriers, courierID string, command func() error, problems map[error]Problem) (CommandResult, error) {
	problem := Problem("")
	if err := command(); err != nil {
		for refusal, code := range problems {
			if errors.Is(err, refusal) {
				problem = code
			}
		}
		if problem == "" {
			return CommandResult{}, err
		}
	}
	view, err := NewGetCourier(couriers).Handle(ctx, courierID)
	if err != nil {
		return CommandResult{}, fmt.Errorf("read the courier back: %w", err)
	}
	return CommandResult{Courier: view, Problem: problem}, nil
}

// SetAvailability switches whether the courier receives offers. Busy is set
// by an assignment only, so the screen may ask for Available or Unavailable.
type SetAvailability struct {
	couriers Couriers
}

func NewSetAvailability(couriers Couriers) SetAvailability {
	return SetAvailability{couriers: couriers}
}

// Handle returns ErrUnknownCommand for Busy, ErrNotCourier and
// ErrAvailabilityConflict for the refusals of delivery; the schema has no
// problem code for them.
func (h SetAvailability) Handle(ctx context.Context, courierID string, availability Availability) (CommandResult, error) {
	if availability != AvailabilityAvailable && availability != AvailabilityUnavailable {
		return CommandResult{}, fmt.Errorf("%w: availability %q", ErrUnknownCommand, availability)
	}
	return courierCommand(ctx, h.couriers, courierID, func() error {
		return h.couriers.SetAvailability(ctx, courierID, availability)
	}, nil)
}

// AcceptOffer accepts a delivery offer addressed to the courier.
type AcceptOffer struct {
	couriers Couriers
}

func NewAcceptOffer(couriers Couriers) AcceptOffer {
	return AcceptOffer{couriers: couriers}
}

// Handle answers an expired, taken or unknown offer as OFFER_NOT_AVAILABLE.
func (h AcceptOffer) Handle(ctx context.Context, courierID, offerID string) (CommandResult, error) {
	return courierCommand(ctx, h.couriers, courierID, func() error {
		return h.couriers.AcceptOffer(ctx, courierID, offerID)
	}, map[error]Problem{ErrOfferNotAvailable: ProblemOfferNotAvailable})
}

// ApplyDeliveryAction confirms the pickup or the handover of the current
// delivery.
type ApplyDeliveryAction struct {
	couriers Couriers
}

func NewApplyDeliveryAction(couriers Couriers) ApplyDeliveryAction {
	return ApplyDeliveryAction{couriers: couriers}
}

// Handle answers a forbidden stage as DELIVERY_STAGE_CONFLICT; an unknown
// delivery stays ErrDeliveryNotFound.
func (h ApplyDeliveryAction) Handle(ctx context.Context, courierID, deliveryID string, action Action) (CommandResult, error) {
	var command func() error
	switch action {
	case ActionConfirmPickup:
		command = func() error { return h.couriers.ConfirmPickup(ctx, courierID, deliveryID) }
	case ActionConfirmDelivery:
		command = func() error { return h.couriers.ConfirmDelivery(ctx, courierID, deliveryID) }
	default:
		return CommandResult{}, fmt.Errorf("%w: action %q", ErrUnknownCommand, action)
	}
	return courierCommand(ctx, h.couriers, courierID, command, map[error]Problem{ErrStageConflict: ProblemDeliveryStageConflict})
}
