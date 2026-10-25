// Package infrastructure adapts the courier port to the courier RPCs of
// delivery.v1.DeliveryService, called through the internal gateway on behalf
// of the user: service token of BFF and user assertion (ADR 32). delivery
// authorizes courier#act on courier_id and checks that the delivery or offer
// belongs to the courier.
package infrastructure

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	deliveryv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/delivery/v1"
)

// Delivery implements applications.Couriers.
type Delivery struct {
	client deliveryv1.DeliveryServiceClient
}

func NewDelivery(conn grpc.ClientConnInterface) *Delivery {
	return &Delivery{client: deliveryv1.NewDeliveryServiceClient(conn)}
}

var _ applications.Couriers = (*Delivery)(nil)

var (
	availabilities = map[deliveryv1.CourierStatus]applications.Availability{
		deliveryv1.CourierStatus_COURIER_STATUS_AVAILABLE:   applications.AvailabilityAvailable,
		deliveryv1.CourierStatus_COURIER_STATUS_BUSY:        applications.AvailabilityBusy,
		deliveryv1.CourierStatus_COURIER_STATUS_UNAVAILABLE: applications.AvailabilityUnavailable,
	}
	deliveryStages = map[deliveryv1.DeliveryState]applications.DeliveryStage{
		deliveryv1.DeliveryState_DELIVERY_STATE_ASSIGNED:  applications.StageAssigned,
		deliveryv1.DeliveryState_DELIVERY_STATE_PICKED_UP: applications.StagePickedUp,
	}
	requestedAvailabilities = map[applications.Availability]deliveryv1.CourierAvailability{
		applications.AvailabilityAvailable:   deliveryv1.CourierAvailability_COURIER_AVAILABILITY_AVAILABLE,
		applications.AvailabilityUnavailable: deliveryv1.CourierAvailability_COURIER_AVAILABILITY_UNAVAILABLE,
	}
)

// State reads the courier status and the active delivery in one call.
func (d *Delivery) State(ctx context.Context, courierID string) (applications.CourierState, error) {
	response, err := d.client.GetCourierCurrentDelivery(ctx, &deliveryv1.GetCourierCurrentDeliveryRequest{CourierId: courierID})
	if err != nil {
		return applications.CourierState{}, courierError("get courier current delivery", err)
	}
	availability, ok := availabilities[response.GetCourierStatus()]
	if !ok {
		return applications.CourierState{}, fmt.Errorf("courier %s in unexpected status %s", courierID, response.GetCourierStatus())
	}
	state := applications.CourierState{Availability: availability}
	if delivery := response.GetDelivery(); delivery != nil {
		stage, ok := deliveryStages[delivery.GetState()]
		if !ok {
			return applications.CourierState{}, fmt.Errorf("current delivery %s in unexpected state %s", delivery.GetDeliveryId(), delivery.GetState())
		}
		state.CurrentDelivery = &applications.Delivery{
			ID:             delivery.GetDeliveryId(),
			OrderID:        delivery.GetOrderId(),
			Stage:          stage,
			PickupAddress:  delivery.GetPickupAddress(),
			DropoffAddress: delivery.GetDropoffAddress(),
		}
	}
	return state, nil
}

func (d *Delivery) Offers(ctx context.Context, courierID string) ([]applications.Offer, error) {
	response, err := d.client.ListAvailableDeliveryOffers(ctx, &deliveryv1.ListAvailableDeliveryOffersRequest{CourierId: courierID})
	if err != nil {
		return nil, courierError("list available delivery offers", err)
	}
	offers := make([]applications.Offer, 0, len(response.GetOffers()))
	for _, offer := range response.GetOffers() {
		offers = append(offers, applications.Offer{
			ID:             offer.GetOfferId(),
			DeliveryID:     offer.GetDeliveryId(),
			OrderID:        offer.GetOrderId(),
			PickupAddress:  offer.GetPickupAddress(),
			DropoffAddress: offer.GetDropoffAddress(),
			ExpiresAt:      time.Unix(offer.GetExpiresAtEpochSeconds(), 0).UTC(),
		})
	}
	return offers, nil
}

func (d *Delivery) SetAvailability(ctx context.Context, courierID string, availability applications.Availability) error {
	requested, ok := requestedAvailabilities[availability]
	if !ok {
		return fmt.Errorf("%w: availability %q", applications.ErrUnknownCommand, availability)
	}
	_, err := d.client.SetCourierAvailability(ctx, &deliveryv1.SetCourierAvailabilityRequest{CourierId: courierID, Availability: requested})
	if status.Code(err) == codes.FailedPrecondition {
		return applications.ErrAvailabilityConflict
	}
	if err != nil {
		return courierError("set courier availability", err)
	}
	return nil
}

// AcceptOffer treats an unknown offer like an expired or taken one: the
// screen offers it from a list that may be stale.
func (d *Delivery) AcceptOffer(ctx context.Context, courierID, offerID string) error {
	_, err := d.client.AcceptDeliveryOffer(ctx, &deliveryv1.AcceptDeliveryOfferRequest{OfferId: offerID, CourierId: courierID})
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.NotFound, codes.FailedPrecondition:
		return applications.ErrOfferNotAvailable
	}
	return courierError("accept delivery offer", err)
}

func (d *Delivery) ConfirmPickup(ctx context.Context, courierID, deliveryID string) error {
	_, err := d.client.ConfirmPickup(ctx, &deliveryv1.ConfirmPickupRequest{DeliveryId: deliveryID, CourierId: courierID})
	return deliveryCommandError("confirm pickup", err)
}

func (d *Delivery) ConfirmDelivery(ctx context.Context, courierID, deliveryID string) error {
	_, err := d.client.ConfirmDelivery(ctx, &deliveryv1.ConfirmDeliveryRequest{DeliveryId: deliveryID, CourierId: courierID})
	return deliveryCommandError("confirm delivery", err)
}

func deliveryCommandError(operation string, err error) error {
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.NotFound:
		return applications.ErrDeliveryNotFound
	case codes.FailedPrecondition:
		return applications.ErrStageConflict
	}
	return courierError(operation, err)
}

// courierError names the refusals every courier RPC shares: without
// courier#act (PERMISSION_DENIED) or an unregistered courier (NOT_FOUND of
// the courier reads) the user is not a courier.
func courierError(operation string, err error) error {
	switch status.Code(err) {
	case codes.NotFound, codes.PermissionDenied:
		return applications.ErrNotCourier
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", applications.ErrInvalidCommand, status.Convert(err).Message())
	}
	return fmt.Errorf("%s: %w", operation, err)
}
