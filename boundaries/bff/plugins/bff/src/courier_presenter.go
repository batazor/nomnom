package main

import (
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

var (
	courierAvailabilities = map[applications.Availability]service.CourierAvailability{
		applications.AvailabilityAvailable:   service.CourierAvailability_COURIER_AVAILABILITY_AVAILABLE,
		applications.AvailabilityBusy:        service.CourierAvailability_COURIER_AVAILABILITY_BUSY,
		applications.AvailabilityUnavailable: service.CourierAvailability_COURIER_AVAILABILITY_UNAVAILABLE,
	}
	requestedCourierAvailabilities = map[service.CourierAvailability]applications.Availability{
		service.CourierAvailability_COURIER_AVAILABILITY_AVAILABLE:   applications.AvailabilityAvailable,
		service.CourierAvailability_COURIER_AVAILABILITY_UNAVAILABLE: applications.AvailabilityUnavailable,
	}
	courierDeliveryStages = map[applications.DeliveryStage]service.CourierDeliveryStage{
		applications.StageAssigned: service.CourierDeliveryStage_COURIER_DELIVERY_STAGE_ASSIGNED,
		applications.StagePickedUp: service.CourierDeliveryStage_COURIER_DELIVERY_STAGE_PICKED_UP,
	}
	courierDeliveryActions = map[service.CourierDeliveryAction]applications.Action{
		service.CourierDeliveryAction_COURIER_DELIVERY_ACTION_CONFIRM_PICKUP:   applications.ActionConfirmPickup,
		service.CourierDeliveryAction_COURIER_DELIVERY_ACTION_CONFIRM_DELIVERY: applications.ActionConfirmDelivery,
	}
	courierProblems = map[applications.Problem]service.CourierProblemCode{
		applications.ProblemOfferNotAvailable:     service.CourierProblemCode_COURIER_PROBLEM_CODE_OFFER_NOT_AVAILABLE,
		applications.ProblemDeliveryStageConflict: service.CourierProblemCode_COURIER_PROBLEM_CODE_DELIVERY_STAGE_CONFLICT,
	}
)

func courierMessage(view applications.CourierView) (*service.Courier, error) {
	availability, err := enumValue(courierAvailabilities, "courier availability", view.Availability)
	if err != nil {
		return nil, err
	}
	message := &service.Courier{
		Availability: availability,
		Offers:       make([]*service.DeliveryOffer, 0, len(view.Offers)),
	}
	for _, offer := range view.Offers {
		message.Offers = append(message.Offers, &service.DeliveryOffer{
			Id:             offer.ID,
			DeliveryId:     offer.DeliveryID,
			OrderId:        offer.OrderID,
			PickupAddress:  offer.PickupAddress,
			DropoffAddress: offer.DropoffAddress,
			ExpiresAt:      timestamp(offer.ExpiresAt),
		})
	}
	if delivery := view.CurrentDelivery; delivery != nil {
		stage, err := enumValue(courierDeliveryStages, "courier delivery stage", delivery.Stage)
		if err != nil {
			return nil, err
		}
		message.CurrentDelivery = &service.CourierDelivery{
			Id:             delivery.ID,
			OrderId:        delivery.OrderID,
			Stage:          stage,
			PickupAddress:  delivery.PickupAddress,
			DropoffAddress: delivery.DropoffAddress,
		}
	}
	return message, nil
}

func courierCommandPayload(result applications.CommandResult) (*service.CourierPayload, error) {
	courier, err := courierMessage(result.Courier)
	if err != nil {
		return nil, err
	}
	payload := &service.CourierPayload{Courier: courier}
	if result.Problem != "" {
		payload.Problem, err = enumValue(courierProblems, "courier problem", result.Problem)
		if err != nil {
			return nil, err
		}
	}
	return payload, nil
}
