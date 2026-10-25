// Package messaging is the Kafka inbound adapter of the order slice (ADR 28):
// Watermill group handlers decode the Event Envelope and the Protobuf payload,
// map them to a Command, call the application handler and classify its error.
// It contains no domain logic; the Router and its lifecycle belong to the
// composition root of orders-consumer.
package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"google.golang.org/protobuf/proto"

	"nomnom/orders/pkg/application"
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
)

// Stream topics of the source aggregates. Names are provisional until the
// integration specification of restaurants and delivery fixes them.
const (
	RestaurantOrderEventsTopic = "restaurants.v1.restaurant-order-events"
	DeliveryEventsTopic        = "delivery.v1.delivery-events"
)

// Handlers are the application handlers the order slice consumes events with.
type Handlers struct {
	ConfirmOrder             application.Handler[confirmorder.Input, confirmorder.Result]
	RejectOrder              application.Handler[rejectorder.Input, rejectorder.Result]
	RecordPreparationStarted application.Handler[recordpreparationstarted.Input, recordpreparationstarted.Result]
	RecordOrderReady         application.Handler[recordorderready.Input, recordorderready.Result]
	RelayPreparationIssue    application.Handler[relaypreparationissue.Input, relaypreparationissue.Result]
	RecordCourierAssignment  application.Handler[recordcourierassignment.Input, recordcourierassignment.Result]
	RecordPickup             application.Handler[recordpickup.Input, recordpickup.Result]
	RecordDeliveryCompletion application.Handler[recorddeliverycompletion.Input, recorddeliverycompletion.Result]
	RecordDeliveryFailure    application.Handler[recorddeliveryfailure.Input, recorddeliveryfailure.Result]
}

// Streams returns one handler group per consumed stream.
func Streams(h Handlers) []messaging.Stream {
	return []messaging.Stream{
		{
			Group: "orders.restaurant-order-events",
			Topic: RestaurantOrderEventsTopic,
			Handlers: []cqrs.GroupEventHandler{
				handle(h.ConfirmOrder.Handle, toConfirmOrder),
				handle(h.RejectOrder.Handle, toRejectOrder),
				handle(h.RecordPreparationStarted.Handle, toRecordPreparationStarted),
				handle(h.RecordOrderReady.Handle, toRecordOrderReady),
				handle(h.RelayPreparationIssue.Handle, toPreparationDelayed),
				handle(h.RelayPreparationIssue.Handle, toPreparationFailed),
			},
		},
		{
			Group: "orders.delivery-events",
			Topic: DeliveryEventsTopic,
			Handlers: []cqrs.GroupEventHandler{
				handle(h.RecordCourierAssignment.Handle, toRecordCourierAssignment),
				handle(h.RecordPickup.Handle, toRecordPickup),
				handle(h.RecordDeliveryCompletion.Handle, toRecordDeliveryCompletion),
				handle(h.RecordDeliveryFailure.Handle, toRecordDeliveryFailure),
			},
		},
	}
}

// handle adapts one application handler to a Watermill group handler. It
// returns nil only after the handler returned, that is after its UoW commit.
func handle[E any, PE interface {
	*E
	proto.Message
}, In, Out any](
	call func(context.Context, In) (Out, error),
	toCommand func(messaging.Envelope, PE) In,
) cqrs.GroupEventHandler {
	return cqrs.NewGroupEventHandler(func(ctx context.Context, event *E) error {
		msg := cqrs.OriginalMessageFromCtx(ctx)
		if msg == nil {
			return messaging.Permanent(fmt.Errorf("no original message in context"))
		}
		envelope, err := messaging.EnvelopeFromMessage(msg)
		if err != nil {
			return err
		}
		out, err := call(ctx, toCommand(envelope, PE(event)))
		if err == nil && isDuplicate(out) {
			messaging.MarkDuplicate(ctx)
		}
		return classify(err)
	})
}

// isDuplicate reports an Inbox duplicate of the reflecting use cases, whose
// Result has the shape of fulfillment.Reflection.
func isDuplicate(out any) bool {
	type reflection = struct {
		Outcome          fulfillment.Outcome
		WorkflowNotified bool
	}
	switch r := out.(type) {
	case confirmorder.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case rejectorder.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recordpreparationstarted.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recordorderready.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recordcourierassignment.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recordpickup.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recorddeliverycompletion.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recorddeliveryfailure.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	default:
		return false
	}
}

// classify maps application and domain errors to the consumer error classes.
func classify(err error) error {
	var invalid order.InvalidTransition
	var guard order.GuardFailed
	switch {
	case err == nil:
		return nil
	case errors.Is(err, application.ErrInvalidInput), errors.Is(err, order.ErrOrderNotFound):
		return messaging.Permanent(err)
	case errors.As(err, &invalid), errors.As(err, &guard):
		return messaging.Business(err)
	default:
		return err
	}
}
