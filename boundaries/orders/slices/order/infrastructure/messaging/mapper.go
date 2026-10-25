package messaging

import (
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recorddeliverycompletion"
	"nomnom/orders/slices/order/applications/recorddeliveryfailure"
	"nomnom/orders/slices/order/applications/recordorderready"
	"nomnom/orders/slices/order/applications/recordpickup"
	"nomnom/orders/slices/order/applications/recordpreparationstarted"
	"nomnom/orders/slices/order/applications/rejectorder"
	"nomnom/orders/slices/order/applications/relaypreparationissue"
	"nomnom/orders/slices/order/domains/order"
	deliveryv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/delivery/v1"
	restaurantsv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/restaurants/v1"
)

// Mapping Transport DTO + Envelope → Command. Pure functions: no storage,
// network, clock or random (ADR 6).

func toConfirmOrder(e messaging.Envelope, event *restaurantsv1.RestaurantOrderAccepted) confirmorder.Input {
	return confirmorder.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt}
}

func toRejectOrder(e messaging.Envelope, event *restaurantsv1.RestaurantOrderRejected) rejectorder.Input {
	return rejectorder.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt, Reason: event.GetReason()}
}

func toRecordPreparationStarted(e messaging.Envelope, event *restaurantsv1.PreparationStarted) recordpreparationstarted.Input {
	return recordpreparationstarted.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt}
}

func toRecordOrderReady(e messaging.Envelope, event *restaurantsv1.RestaurantOrderReady) recordorderready.Input {
	return recordorderready.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt}
}

func toPreparationDelayed(e messaging.Envelope, event *restaurantsv1.PreparationDelayed) relaypreparationissue.Input {
	in := relaypreparationissue.Input{
		EventID:    e.EventID,
		OrderID:    order.ID(event.GetOrderId()),
		Kind:       fulfillment.KindPreparationDelayed,
		OccurredAt: e.OccurredAt,
	}
	if event.GetExpectedReadyAt() != nil {
		in.ExpectedReadyAt = event.GetExpectedReadyAt().AsTime()
	}
	return in
}

func toPreparationFailed(e messaging.Envelope, event *restaurantsv1.PreparationFailed) relaypreparationissue.Input {
	return relaypreparationissue.Input{
		EventID:    e.EventID,
		OrderID:    order.ID(event.GetOrderId()),
		Kind:       fulfillment.KindPreparationFailed,
		OccurredAt: e.OccurredAt,
		Reason:     event.GetReason(),
	}
}

func toRecordCourierAssignment(e messaging.Envelope, event *deliveryv1.CourierAssignedToDelivery) recordcourierassignment.Input {
	return recordcourierassignment.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt}
}

func toRecordPickup(e messaging.Envelope, event *deliveryv1.DeliveryPickedUp) recordpickup.Input {
	return recordpickup.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt}
}

func toRecordDeliveryCompletion(e messaging.Envelope, event *deliveryv1.DeliveryCompleted) recorddeliverycompletion.Input {
	return recorddeliverycompletion.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt}
}

func toRecordDeliveryFailure(e messaging.Envelope, event *deliveryv1.DeliveryFailed) recorddeliveryfailure.Input {
	return recorddeliveryfailure.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt, Reason: event.GetReason()}
}
