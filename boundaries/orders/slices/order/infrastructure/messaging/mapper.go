package messaging

import (
	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recordcourierrelease"
	"nomnom/orders/slices/order/applications/recorddeliverycompletion"
	"nomnom/orders/slices/order/applications/recorddeliveryfailure"
	"nomnom/orders/slices/order/applications/recordorderready"
	"nomnom/orders/slices/order/applications/recordpickup"
	"nomnom/orders/slices/order/applications/recordpreparationdelay"
	"nomnom/orders/slices/order/applications/recordpreparationstarted"
	"nomnom/orders/slices/order/applications/rejectorder"
	"nomnom/orders/slices/order/applications/relaypreparationissue"
	"nomnom/orders/slices/order/applications/startfulfillment"
	"nomnom/orders/slices/order/domains/order"
	deliveryv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/delivery/v1"
	restaurantsv1 "nomnom/orders/slices/order/infrastructure/messaging/gen/nomnom/restaurants/v1"
)

// Mapping Transport DTO + Envelope → Command. Pure functions: no storage,
// network, clock or random (ADR 6).

// expectedTime reads a deadline of an event: an absent one is not named (zero).
func expectedTime(at *timestamppb.Timestamp) time.Time {
	if at == nil {
		return time.Time{}
	}
	return at.AsTime()
}

func toConfirmOrder(e messaging.Envelope, event *restaurantsv1.RestaurantOrderAccepted) confirmorder.Input {
	return confirmorder.Input{
		EventID:         e.EventID,
		OrderID:         order.ID(event.GetOrderId()),
		OccurredAt:      e.OccurredAt,
		ExpectedReadyAt: expectedTime(event.GetExpectedReadyAt()),
	}
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

func toRecordPreparationDelay(e messaging.Envelope, event *restaurantsv1.PreparationDelayed) recordpreparationdelay.Input {
	return recordpreparationdelay.Input{
		EventID:         e.EventID,
		OrderID:         order.ID(event.GetOrderId()),
		OccurredAt:      e.OccurredAt,
		ExpectedReadyAt: expectedTime(event.GetExpectedReadyAt()),
	}
}

func toPreparationFailed(e messaging.Envelope, event *restaurantsv1.PreparationFailed) relaypreparationissue.Input {
	return relaypreparationissue.Input{
		EventID:    e.EventID,
		OrderID:    order.ID(event.GetOrderId()),
		OccurredAt: e.OccurredAt,
		Reason:     event.GetReason(),
	}
}

func toRecordCourierAssignment(e messaging.Envelope, event *deliveryv1.CourierAssignedToDelivery) recordcourierassignment.Input {
	return recordcourierassignment.Input{
		EventID:          e.EventID,
		OrderID:          order.ID(event.GetOrderId()),
		OccurredAt:       e.OccurredAt,
		DeliveryVersion:  e.AggregateVersion,
		DeliveryID:       event.GetDeliveryId(),
		CourierID:        event.GetCourierId(),
		ExpectedPickupAt: expectedTime(event.GetExpectedPickupAt()),
	}
}

func toCourierUnassignment(e messaging.Envelope, event *deliveryv1.CourierUnassignedFromDelivery) recordcourierrelease.Input {
	return recordcourierrelease.Input{
		EventID:         e.EventID,
		OrderID:         order.ID(event.GetOrderId()),
		OccurredAt:      e.OccurredAt,
		DeliveryVersion: e.AggregateVersion,
		DeliveryID:      event.GetDeliveryId(),
		CourierID:       event.GetCourierId(),
	}
}

// toDeliveryCancellation drops the reason: no fulfillment fact of orders
// derives from it (the cancellation is a compensation orders requested).
func toDeliveryCancellation(e messaging.Envelope, event *deliveryv1.DeliveryCancelled) recordcourierrelease.Input {
	return recordcourierrelease.Input{
		EventID:           e.EventID,
		OrderID:           order.ID(event.GetOrderId()),
		OccurredAt:        e.OccurredAt,
		DeliveryVersion:   e.AggregateVersion,
		DeliveryID:        event.GetDeliveryId(),
		DeliveryCancelled: true,
	}
}

func toRecordPickup(e messaging.Envelope, event *deliveryv1.DeliveryPickedUp) recordpickup.Input {
	return recordpickup.Input{
		EventID:             e.EventID,
		OrderID:             order.ID(event.GetOrderId()),
		OccurredAt:          e.OccurredAt,
		ExpectedDeliveredAt: expectedTime(event.GetExpectedDeliveredAt()),
	}
}

func toRecordDeliveryCompletion(e messaging.Envelope, event *deliveryv1.DeliveryCompleted) recorddeliverycompletion.Input {
	return recorddeliverycompletion.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt}
}

func toRecordDeliveryFailure(e messaging.Envelope, event *deliveryv1.DeliveryFailed) recorddeliveryfailure.Input {
	return recorddeliveryfailure.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt, Reason: event.GetReason()}
}

// toSyncOrderRelationships takes only the Order id: the sync reads the rest
// from the Order.
func toSyncOrderRelationships(_ messaging.Envelope, event *ordersv1.OrderRelationshipsChanged) syncorderrelationships.Input {
	return syncorderrelationships.Input{OrderID: order.ID(event.GetOrderId())}
}

// toStartFulfillment carries the Delivery Window of a Scheduled Order: the
// workflow chooses its route by it (FR-415).
func toStartFulfillment(e messaging.Envelope, event *ordersv1.OrderPlaced) startfulfillment.Input {
	in := startfulfillment.Input{EventID: e.EventID, OrderID: order.ID(event.GetOrderId()), OccurredAt: e.OccurredAt}
	if window := event.GetDeliveryWindow(); window != nil {
		in.DeliveryWindowStart = window.GetStart().AsTime()
		in.DeliveryWindowEnd = window.GetEnd().AsTime()
	}
	return in
}
