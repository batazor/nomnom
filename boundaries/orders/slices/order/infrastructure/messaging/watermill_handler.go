// Package messaging is the Kafka inbound adapter of the order slice (ADR 28):
// Watermill group handlers decode the Event Envelope and the Protobuf payload,
// map them to a Command, call the application handler and classify its error.
// It contains no domain logic; the Router and its lifecycle belong to the
// composition root of orders-consumer.
package messaging

import (
	"context"
	"fmt"

	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"google.golang.org/protobuf/proto"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"

	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"

	"nomnom/orders/infrastructure/messaging"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/fulfillment"
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
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
)

// Stream topics of the source aggregates. Names are provisional until the
// integration specification of restaurants and delivery fixes them.
const (
	RestaurantOrderEventsTopic = "restaurants.v1.restaurant-order-events"
	DeliveryEventsTopic        = "delivery.v1.delivery-events"
)

// Handlers are the application handlers the order slice consumes events with.
type Handlers struct {
	ConfirmOrder             usecase.Handler[confirmorder.Input, confirmorder.Result]
	RejectOrder              usecase.Handler[rejectorder.Input, rejectorder.Result]
	RecordPreparationStarted usecase.Handler[recordpreparationstarted.Input, recordpreparationstarted.Result]
	RecordOrderReady         usecase.Handler[recordorderready.Input, recordorderready.Result]
	RecordPreparationDelay   usecase.Handler[recordpreparationdelay.Input, recordpreparationdelay.Result]
	RelayPreparationIssue    usecase.Handler[relaypreparationissue.Input, relaypreparationissue.Result]
	RecordCourierAssignment  usecase.Handler[recordcourierassignment.Input, recordcourierassignment.Result]
	RecordCourierRelease     usecase.Handler[recordcourierrelease.Input, recordcourierrelease.Result]
	RecordPickup             usecase.Handler[recordpickup.Input, recordpickup.Result]
	RecordDeliveryCompletion usecase.Handler[recorddeliverycompletion.Input, recorddeliverycompletion.Result]
	RecordDeliveryFailure    usecase.Handler[recorddeliveryfailure.Input, recorddeliveryfailure.Result]
	SyncOrderRelationships   usecase.Handler[syncorderrelationships.Input, syncorderrelationships.Result]
	StartFulfillment         usecase.Handler[startfulfillment.Input, startfulfillment.Result]
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
				handle(h.RecordPreparationDelay.Handle, toRecordPreparationDelay),
				handle(h.RelayPreparationIssue.Handle, toPreparationFailed),
				// The confirmation of CancelRestaurantOrder, which the workflow itself
				// called: it already has the result and the Order changes nothing.
				acknowledge[restaurantsv1.RestaurantOrderCancelled](),
			},
		},
		{
			Group: "orders.delivery-events",
			Topic: DeliveryEventsTopic,
			Handlers: []cqrs.GroupEventHandler{
				handle(h.RecordCourierAssignment.Handle, toRecordCourierAssignment),
				handle(h.RecordCourierRelease.Handle, toCourierUnassignment),
				handle(h.RecordCourierRelease.Handle, toDeliveryCancellation),
				handle(h.RecordPickup.Handle, toRecordPickup),
				handle(h.RecordDeliveryCompletion.Handle, toRecordDeliveryCompletion),
				handle(h.RecordDeliveryFailure.Handle, toRecordDeliveryFailure),
			},
		},
		{
			// Own Integration Events of orders (ADR 5): OrderPlaced starts the
			// workflow; OrderCompleted and OrderCancelled are acknowledged
			// explicitly, otherwise rejectUnknownEvents would dead-letter them.
			Group: "orders.order-events",
			Topic: outbound.OrderEventsTopic,
			Handlers: []cqrs.GroupEventHandler{
				handle(h.StartFulfillment.Handle, toStartFulfillment),
				acknowledge[ordersv1.OrderCompleted](),
				acknowledge[ordersv1.OrderCancelled](),
			},
		},
		{
			// Internal stream of orders (ADR 18): the sync reads the Order state,
			// so it needs no Inbox — a repeat writes the same relationships.
			Group: "orders.order-relationships",
			Topic: outbound.OrderRelationshipsTopic,
			Handlers: []cqrs.GroupEventHandler{
				handle(h.SyncOrderRelationships.Handle, toSyncOrderRelationships),
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

// acknowledge confirms an event of the stream that needs no action.
func acknowledge[E any]() cqrs.GroupEventHandler {
	return cqrs.NewGroupEventHandler(func(context.Context, *E) error { return nil })
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
	case recordpreparationdelay.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recordcourierassignment.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recordcourierrelease.Result:
		return r.Outcome == fulfillment.OutcomeDuplicate
	case recordpickup.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recorddeliverycompletion.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case recorddeliveryfailure.Result:
		return reflection(r).Outcome == fulfillment.OutcomeDuplicate
	case startfulfillment.Result:
		return r.Outcome == fulfillment.OutcomeDuplicate
	default:
		return false
	}
}

// transient leaves an error unmarked: the consumer retries it with backoff.
func transient(err error) error { return err }

// classByKind is the consumer error class (ADR 28) of every
// usecase.ErrorKind: redelivery cannot fix invalid input, a missing Order
// or a denial; a domain refusal is an expected business outcome; the rest is
// retried.
var classByKind = map[usecase.ErrorKind]func(error) error{
	usecase.KindInvalidInput:  messaging.Permanent,
	usecase.KindNotFound:      messaging.Permanent,
	usecase.KindPermission:    messaging.Permanent,
	usecase.KindDomainRule:    messaging.Business,
	usecase.KindAlreadyExists: messaging.Business,
	usecase.KindConflict:      transient,
	usecase.KindUnavailable:   transient,
	usecase.KindInternal:      transient,
}

// classify marks a handler error with the class of its kind.
func classify(err error) error {
	if err == nil {
		return nil
	}
	mark, ok := classByKind[errorkind.Classify(err)]
	if !ok {
		return err
	}
	return mark(err)
}
