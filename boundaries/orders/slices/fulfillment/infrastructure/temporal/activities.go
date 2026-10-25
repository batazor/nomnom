package temporal

import (
	"context"

	"go.temporal.io/sdk/temporal"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/fulfillment/applications/neighbours"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/domains/order"
)

type CompleteOrder interface {
	Handle(ctx context.Context, in completeorder.Input) (completeorder.Result, error)
}

type FailOrCancelOrder interface {
	Handle(ctx context.Context, in failorcancelorder.Input) (failorcancelorder.Result, error)
}

// Activities are the only side effects of the workflow. They call the client
// ports of applications/neighbours and the use cases of the order slice.
// Errors of invalid input, a missing Order or a forbidden transition are not
// retried.
type Activities struct {
	Orders      neighbours.Orders
	Payments    neighbours.Payments
	Restaurants neighbours.Restaurants
	Delivery    neighbours.Delivery
	Complete    CompleteOrder
	Finalize    FailOrCancelOrder
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

func (a *Activities) RequestRestaurantOrder(ctx context.Context, in StepInput) error {
	request, err := a.restaurantOrder(ctx, in)
	if err != nil {
		return err
	}
	return classify(a.Restaurants.RequestOrder(ctx, request))
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

func (a *Activities) ReplanDelivery(ctx context.Context, in StepInput) error {
	request, err := a.delivery(ctx, in)
	if err != nil {
		return err
	}
	return classify(a.Delivery.Replan(ctx, request))
}

func (a *Activities) CancelDelivery(ctx context.Context, in StepInput) error {
	request, err := a.delivery(ctx, in)
	if err != nil {
		return err
	}
	return classify(a.Delivery.Cancel(ctx, request))
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
// from its snapshot: Activities load → build the request → call → classify.
func request[R any](ctx context.Context, orders neighbours.Orders, in StepInput, build func(*order.Order, string) R) (R, error) {
	var zero R
	if in.OrderID == "" || in.IdempotencyKey == "" {
		return zero, classify(application.InvalidInput("step", "order_id and idempotency_key are required"))
	}
	current, err := orders.Get(ctx, order.ID(in.OrderID))
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
