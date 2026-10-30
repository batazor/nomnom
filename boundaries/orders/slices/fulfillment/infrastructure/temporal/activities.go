package temporal

import (
	"context"
	"errors"

	"go.temporal.io/sdk/temporal"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/fulfillment/applications/neighbours"
	"nomnom/orders/slices/fulfillment/applications/orders"
)

// Activities are the only side effects of the workflow. They call the client
// ports of applications/neighbours and the port of applications/orders. Errors
// of invalid input, a missing Order or a forbidden transition are not retried.
type Activities struct {
	Orders      orders.Orders
	Payments    neighbours.Payments
	Restaurants neighbours.Restaurants
	Delivery    neighbours.Delivery
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
	request, err := request(ctx, a.Orders, in, func(current orders.Order, key string) neighbours.RestaurantOrderRequest {
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
		return RestaurantOrderRequested{}, classify(usecase.InvalidInput("step", "planned_ready_at is required for a scheduled order"))
	}
	id, err := a.Restaurants.RequestOrder(ctx, request)
	return RestaurantOrderRequested{RestaurantOrderID: id}, classify(err)
}

// QueueRestaurantOrder moves the accepted Restaurant Order of a Scheduled
// Order to the Kitchen Queue at start_at (FR-224).
func (a *Activities) QueueRestaurantOrder(ctx context.Context, in StepInput) error {
	if in.OrderID == "" || in.IdempotencyKey == "" || in.RestaurantOrderID == "" {
		return classify(usecase.InvalidInput("step", "order_id, idempotency_key and restaurant_order_id are required"))
	}
	return classify(a.Restaurants.QueueOrder(ctx, neighbours.QueueRequest{
		OrderID:           in.OrderID,
		RestaurantOrderID: in.RestaurantOrderID,
		IdempotencyKey:    in.IdempotencyKey,
	}))
}

// RecordRestaurantPickup tells the kitchen the courier took the Order: restaurants
// reads nothing from delivery.
func (a *Activities) RecordRestaurantPickup(ctx context.Context, in StepInput) error {
	if in.OrderID == "" || in.IdempotencyKey == "" || in.DecidedAt.IsZero() {
		return classify(usecase.InvalidInput("step", "order_id, idempotency_key and decided_at are required"))
	}
	return classify(a.Restaurants.RecordPickup(ctx, neighbours.RestaurantPickupRequest{
		OrderID:    in.OrderID,
		PickedUpAt: in.DecidedAt,
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
		return Plan{}, classify(usecase.InvalidInput("step", "order_id and idempotency_key are required"))
	}
	plan, err := a.Orders.PlanSchedule(ctx, in.OrderID, in.PlannedReadyAt)
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

// RecordPaymentAuthorization records the payment on the Order after the approved Authorization; a repeat after commit is a no-op. The
// decision moment is the workflow time in the input and is required.
func (a *Activities) RecordPaymentAuthorization(ctx context.Context, in StepInput) error {
	if in.DecidedAt.IsZero() {
		return classify(usecase.InvalidInput("step", "decided_at is required"))
	}
	return classify(a.Orders.RecordPaymentAuthorization(ctx, in.OrderID, in.DecidedAt))
}

// CompleteOrder completes the Order; a repeat after commit is a
// no-op. The completion moment is the workflow time in the input, identical on
// every attempt, and is required.
func (a *Activities) CompleteOrder(ctx context.Context, in StepInput) error {
	if in.DecidedAt.IsZero() {
		return classify(usecase.InvalidInput("step", "decided_at is required"))
	}
	return classify(a.Orders.Complete(ctx, in.OrderID, in.DecidedAt))
}

// FailOrCancelOrder records the workflow decision on the Order.
func (a *Activities) FailOrCancelOrder(ctx context.Context, in FinalizeInput) error {
	return classify(a.Orders.FailOrCancel(ctx, orders.FinalDecision{
		OrderID:    in.OrderID,
		Outcome:    in.Outcome,
		ReasonCode: in.ReasonCode,
		DecidedAt:  in.DecidedAt,
	}))
}

// request loads the Order of a step and builds the request to a neighbour
// from it: Activities load → build the request → call → classify.
// Data of the Order, such as its market_id (ADR 41), is read here on every
// attempt and never added to Input, StepInput or workflow state: workflows
// started before a field existed have no such input in their Event History,
// while the stored Order always has it, so running and replayed workflows stay
// deterministic and need no versioning.
func request[R any](ctx context.Context, port orders.Orders, in StepInput, build func(orders.Order, string) R) (R, error) {
	var zero R
	if in.OrderID == "" || in.IdempotencyKey == "" {
		return zero, classify(usecase.InvalidInput("step", "order_id and idempotency_key are required"))
	}
	current, err := port.Get(ctx, in.OrderID)
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

// classify reports an error that a retry cannot fix as a non-retryable
// application error of its type: invalid input, a missing Order or a step the
// Order refuses. Everything else, including transport failures of neighbours,
// is returned as is and retried by the Retry Policy.
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, usecase.ErrInvalidInput):
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeInvalidInput, err)
	case errors.Is(err, orders.ErrOrderNotFound):
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeOrderNotFound, err)
	case errors.Is(err, orders.ErrStepRefused):
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeInvalidTransition, err)
	default:
		return err
	}
}
