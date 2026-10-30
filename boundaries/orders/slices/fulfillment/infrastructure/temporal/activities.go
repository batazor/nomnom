package temporal

import (
	"context"

	"go.temporal.io/sdk/temporal"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/fulfillment/applications/neighbours"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/applications/getfulfillmentorder"
	"nomnom/orders/slices/order/applications/planschedule"
	"nomnom/orders/slices/order/applications/recordpaymentauthorization"
	"nomnom/orders/slices/order/domains/order"
)

type CompleteOrder interface {
	Handle(ctx context.Context, in completeorder.Input) (completeorder.Result, error)
}

type RecordPaymentAuthorization interface {
	Handle(ctx context.Context, in recordpaymentauthorization.Input) (recordpaymentauthorization.Result, error)
}

type FailOrCancelOrder interface {
	Handle(ctx context.Context, in failorcancelorder.Input) (failorcancelorder.Result, error)
}

// PlanSchedule is the Plan Schedule use case of the order slice (FR-677).
type PlanSchedule interface {
	Handle(ctx context.Context, in planschedule.Input) (order.SchedulePlan, error)
}

// GetFulfillmentOrder is the query of the order slice that gives the data of the
// stored Order to build requests to neighbours from.
type GetFulfillmentOrder interface {
	Handle(ctx context.Context, q getfulfillmentorder.Query) (getfulfillmentorder.View, error)
}

// Activities are the only side effects of the workflow. They call the client
// ports of applications/neighbours and the use cases of the order slice.
// Errors of invalid input, a missing Order or a forbidden transition are not
// retried.
type Activities struct {
	Orders        GetFulfillmentOrder
	Payments      neighbours.Payments
	Restaurants   neighbours.Restaurants
	Delivery      neighbours.Delivery
	RecordPayment RecordPaymentAuthorization
	Complete      CompleteOrder
	Finalize      FailOrCancelOrder
	Plan          PlanSchedule
}

func (a *Activities) AuthorizePayment(ctx context.Context, in StepInput) (PaymentDecision, error) {
	request, err := a.payment(ctx, in)
	if err != nil {
		return PaymentDecision{}, err
	}
	decision, err := a.Payments.Authorize(ctx, request)
	return decision, classify(err)
}

func (a *Activities) CapturePayment(ctx context.Context, in StepInput) (PaymentDecision, error) {
	request, err := a.payment(ctx, in)
	if err != nil {
		return PaymentDecision{}, err
	}
	decision, err := a.Payments.Capture(ctx, request)
	return decision, classify(err)
}

func (a *Activities) CancelPaymentAuthorization(ctx context.Context, in StepInput) error {
	request, err := a.payment(ctx, in)
	if err != nil {
		return err
	}
	return classify(a.Payments.CancelAuthorization(ctx, request))
}

func (a *Activities) RefundPayment(ctx context.Context, in StepInput) error {
	request, err := a.payment(ctx, in)
	if err != nil {
		return err
	}
	return classify(a.Payments.Refund(ctx, request))
}

// RequestRestaurantOrder sends the Order to restaurants and returns the
// restaurant_order_id. A Scheduled Order carries its Delivery Window and the
// planned_ready_at of the first plan from the input (FR-683).
func (a *Activities) RequestRestaurantOrder(ctx context.Context, in StepInput) (RestaurantOrderRequested, error) {
	request, err := request(ctx, a.Orders, in, func(current getfulfillmentorder.View, key string) neighbours.RestaurantOrderRequest {
		r := neighbours.NewRestaurantOrderRequest(current, key)
		if current.DeliveryWindow != nil {
			r.Schedule = &neighbours.RestaurantOrderSchedule{
				WindowStart:    current.DeliveryWindow.Start,
				WindowEnd:      current.DeliveryWindow.End,
				PlannedReadyAt: in.PlannedReadyAt,
			}
		}
		return r
	})
	if err != nil {
		return RestaurantOrderRequested{}, err
	}
	if request.Schedule != nil && request.Schedule.PlannedReadyAt.IsZero() {
		return RestaurantOrderRequested{}, classify(application.InvalidInput("step", "planned_ready_at is required for a scheduled order"))
	}
	id, err := a.Restaurants.RequestOrder(ctx, request)
	return RestaurantOrderRequested{RestaurantOrderID: id}, classify(err)
}

// QueueRestaurantOrder moves the accepted Restaurant Order of a Scheduled
// Order to the Kitchen Queue at start_at (FR-224).
func (a *Activities) QueueRestaurantOrder(ctx context.Context, in StepInput) error {
	if in.OrderID == "" || in.IdempotencyKey == "" || in.RestaurantOrderID == "" {
		return classify(application.InvalidInput("step", "order_id, idempotency_key and restaurant_order_id are required"))
	}
	return classify(a.Restaurants.QueueOrder(ctx, neighbours.QueueRequest{
		OrderID:           in.OrderID,
		RestaurantOrderID: in.RestaurantOrderID,
		IdempotencyKey:    in.IdempotencyKey,
	}))
}

// ConfirmSlotReservation confirms the Delivery Window held by Place Order
// after the payment is recorded (FR-682).
func (a *Activities) ConfirmSlotReservation(ctx context.Context, in StepInput) (SlotDecision, error) {
	request, err := request(ctx, a.Orders, in, neighbours.NewSlotRequest)
	if err != nil {
		return SlotDecision{}, err
	}
	decision, err := a.Restaurants.ConfirmSlot(ctx, request)
	return decision, classify(err)
}

// ReleaseSlotReservation is the compensation of the slot reservation.
func (a *Activities) ReleaseSlotReservation(ctx context.Context, in StepInput) error {
	request, err := request(ctx, a.Orders, in, neighbours.NewSlotRequest)
	if err != nil {
		return err
	}
	return classify(a.Restaurants.ReleaseSlot(ctx, request))
}

// PlanSchedule runs the Plan Schedule use case on fresh estimates; the replan
// passes the planned_ready_at of the first plan, which does not move.
func (a *Activities) PlanSchedule(ctx context.Context, in StepInput) (Plan, error) {
	if in.OrderID == "" || in.IdempotencyKey == "" {
		return Plan{}, classify(application.InvalidInput("step", "order_id and idempotency_key are required"))
	}
	plan, err := a.Plan.Handle(ctx, planschedule.Input{OrderID: order.ID(in.OrderID), PlannedReadyAt: in.PlannedReadyAt})
	if err != nil {
		return Plan{}, classify(err)
	}
	return Plan{
		PlannedReadyAt:   plan.PlannedReadyAt,
		StartAt:          plan.StartAt,
		CourierRequestAt: plan.CourierRequestAt,
		ReplanAt:         plan.ReplanAt,
	}, nil
}

func (a *Activities) CancelRestaurantOrder(ctx context.Context, in StepInput) error {
	request, err := a.restaurantOrder(ctx, in)
	if err != nil {
		return err
	}
	return classify(a.Restaurants.CancelOrder(ctx, request))
}

func (a *Activities) CreateDelivery(ctx context.Context, in StepInput) error {
	request, err := a.delivery(ctx, in)
	if err != nil {
		return err
	}
	return classify(a.Delivery.Create(ctx, request))
}

func (a *Activities) CancelDelivery(ctx context.Context, in StepInput) error {
	request, err := a.delivery(ctx, in)
	if err != nil {
		return err
	}
	return classify(a.Delivery.Cancel(ctx, request))
}

// RecordPaymentAuthorization runs the recordpaymentauthorization use case
// after the approved Authorization; a repeat after commit is a no-op. The
// decision moment is the workflow time in the input and is required.
func (a *Activities) RecordPaymentAuthorization(ctx context.Context, in StepInput) error {
	if in.DecidedAt.IsZero() {
		return classify(application.InvalidInput("step", "decided_at is required"))
	}
	_, err := a.RecordPayment.Handle(ctx, recordpaymentauthorization.Input{OrderID: order.ID(in.OrderID), AuthorizedAt: in.DecidedAt})
	return classify(err)
}

// CompleteOrder runs the completeorder use case; a repeat after commit is a
// no-op. The completion moment is the workflow time in the input, identical on
// every attempt, and is required.
func (a *Activities) CompleteOrder(ctx context.Context, in StepInput) error {
	if in.DecidedAt.IsZero() {
		return classify(application.InvalidInput("step", "decided_at is required"))
	}
	_, err := a.Complete.Handle(ctx, completeorder.Input{OrderID: order.ID(in.OrderID), CompletedAt: in.DecidedAt})
	return classify(err)
}

// FailOrCancelOrder runs the failorcancelorder use case with the workflow decision.
func (a *Activities) FailOrCancelOrder(ctx context.Context, in FinalizeInput) error {
	outcome := order.OutcomeFailure
	if in.Outcome == OutcomeCancellation {
		outcome = order.OutcomeCancellation
	}
	_, err := a.Finalize.Handle(ctx, failorcancelorder.Input{
		OrderID:    order.ID(in.OrderID),
		Outcome:    outcome,
		ReasonCode: in.ReasonCode,
		DecidedAt:  in.DecidedAt,
	})
	return classify(err)
}

// request loads the Order of a step and builds the request to a neighbour
// from its view: Activities load → build the request → call → classify.
// Data of the Order, such as its market_id (ADR 41), is read here on every
// attempt and never added to Input, StepInput or workflow state: workflows
// started before a field existed have no such input in their Event History,
// while the stored Order always has it, so running and replayed workflows stay
// deterministic and need no versioning.
func request[R any](ctx context.Context, orders GetFulfillmentOrder, in StepInput, build func(getfulfillmentorder.View, string) R) (R, error) {
	var zero R
	if in.OrderID == "" || in.IdempotencyKey == "" {
		return zero, classify(application.InvalidInput("step", "order_id and idempotency_key are required"))
	}
	current, err := orders.Handle(ctx, getfulfillmentorder.Query{OrderID: order.ID(in.OrderID)})
	if err != nil {
		return zero, classify(err)
	}
	return build(current, in.IdempotencyKey), nil
}

func (a *Activities) payment(ctx context.Context, in StepInput) (neighbours.PaymentRequest, error) {
	return request(ctx, a.Orders, in, neighbours.NewPaymentRequest)
}

func (a *Activities) restaurantOrder(ctx context.Context, in StepInput) (neighbours.RestaurantOrderRequest, error) {
	return request(ctx, a.Orders, in, neighbours.NewRestaurantOrderRequest)
}

func (a *Activities) delivery(ctx context.Context, in StepInput) (neighbours.DeliveryRequest, error) {
	return request(ctx, a.Orders, in, neighbours.NewDeliveryRequest)
}

// retryability is how an Activity reports an error kind: a non-retryable
// application error of the given type, or the error as is, retried by the
// Retry Policy.
type retryability struct {
	nonRetryable bool
	errType      string
}

// retryabilityByKind covers every application.ErrorKind: a retry cannot fix
// invalid input, a missing Order or a refusal of the domain; everything else,
// including transport failures of neighbours, stays retryable.
var retryabilityByKind = map[application.ErrorKind]retryability{
	application.KindInvalidInput:  {nonRetryable: true, errType: ErrTypeInvalidInput},
	application.KindNotFound:      {nonRetryable: true, errType: ErrTypeOrderNotFound},
	application.KindDomainRule:    {nonRetryable: true, errType: ErrTypeInvalidTransition},
	application.KindAlreadyExists: {},
	application.KindConflict:      {},
	application.KindUnavailable:   {},
	application.KindPermission:    {},
	application.KindInternal:      {},
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	if r := retryabilityByKind[errorkind.Classify(err)]; r.nonRetryable {
		return temporal.NewNonRetryableApplicationError(err.Error(), r.errType, err)
	}
	return err
}
