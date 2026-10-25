package temporal

import (
	"context"
	"errors"

	"go.temporal.io/sdk/temporal"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/domains/order"
)

// Orders reads the Order whose snapshot builds requests to neighbouring contexts.
type Orders interface {
	Get(ctx context.Context, id order.ID) (*order.Order, error)
}

type PaymentRequest struct {
	OrderID        string
	CustomerID     string
	Amount         string
	Currency       string
	IdempotencyKey string
}

// Payments is the client port of the business API of payments, called through
// the internal gateway under the worker service identity.
type Payments interface {
	Authorize(ctx context.Context, request PaymentRequest) (PaymentDecision, error)
	Capture(ctx context.Context, request PaymentRequest) (PaymentDecision, error)
	CancelAuthorization(ctx context.Context, request PaymentRequest) error
	Refund(ctx context.Context, request PaymentRequest) error
}

type RestaurantOrderItem struct {
	MenuItemID string
	Quantity   int
}

type RestaurantOrderRequest struct {
	OrderID        string
	RestaurantID   string
	Items          []RestaurantOrderItem
	IdempotencyKey string
}

// Restaurants is the client port of restaurants. Acceptance arrives later as
// RestaurantOrderAccepted or RestaurantOrderRejected.
type Restaurants interface {
	RequestOrder(ctx context.Context, request RestaurantOrderRequest) error
	CancelOrder(ctx context.Context, request RestaurantOrderRequest) error
}

type DeliveryRequest struct {
	OrderID         string
	RestaurantID    string
	DeliveryAddress string
	IdempotencyKey  string
}

// Delivery is the client port of delivery. Assignment, Pickup and Completion
// arrive later as Integration Events.
type Delivery interface {
	Create(ctx context.Context, request DeliveryRequest) error
	Replan(ctx context.Context, request DeliveryRequest) error
	Cancel(ctx context.Context, request DeliveryRequest) error
}

type CompleteOrder interface {
	Handle(ctx context.Context, in completeorder.Input) (completeorder.Result, error)
}

type FailOrCancelOrder interface {
	Handle(ctx context.Context, in failorcancelorder.Input) (failorcancelorder.Result, error)
}

// Activities are the only side effects of the workflow. Errors of invalid
// input, a missing Order or a forbidden transition are not retried.
type Activities struct {
	Orders      Orders
	Payments    Payments
	Restaurants Restaurants
	Delivery    Delivery
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

// CompleteOrder runs the completeorder use case; a repeat after commit is a no-op.
func (a *Activities) CompleteOrder(ctx context.Context, in StepInput) error {
	_, err := a.Complete.Handle(ctx, completeorder.Input{OrderID: order.ID(in.OrderID)})
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

func (a *Activities) load(ctx context.Context, in StepInput) (*order.Order, error) {
	if in.OrderID == "" || in.IdempotencyKey == "" {
		return nil, classify(application.InvalidInput("step", "order_id and idempotency_key are required"))
	}
	current, err := a.Orders.Get(ctx, order.ID(in.OrderID))
	return current, classify(err)
}

func (a *Activities) payment(ctx context.Context, in StepInput) (PaymentRequest, error) {
	current, err := a.load(ctx, in)
	if err != nil {
		return PaymentRequest{}, err
	}
	return PaymentRequest{
		OrderID:        in.OrderID,
		CustomerID:     string(current.CustomerID()),
		Amount:         current.Total().Amount().String(),
		Currency:       current.Total().Currency(),
		IdempotencyKey: in.IdempotencyKey,
	}, nil
}

func (a *Activities) restaurantOrder(ctx context.Context, in StepInput) (RestaurantOrderRequest, error) {
	current, err := a.load(ctx, in)
	if err != nil {
		return RestaurantOrderRequest{}, err
	}
	items := make([]RestaurantOrderItem, 0, len(current.Items()))
	for _, item := range current.Items() {
		items = append(items, RestaurantOrderItem{MenuItemID: string(item.MenuItemID()), Quantity: item.Quantity().Value()})
	}
	return RestaurantOrderRequest{
		OrderID:        in.OrderID,
		RestaurantID:   string(current.RestaurantID()),
		Items:          items,
		IdempotencyKey: in.IdempotencyKey,
	}, nil
}

func (a *Activities) delivery(ctx context.Context, in StepInput) (DeliveryRequest, error) {
	current, err := a.load(ctx, in)
	if err != nil {
		return DeliveryRequest{}, err
	}
	return DeliveryRequest{
		OrderID:         in.OrderID,
		RestaurantID:    string(current.RestaurantID()),
		DeliveryAddress: current.DeliveryAddress().String(),
		IdempotencyKey:  in.IdempotencyKey,
	}, nil
}

// classify marks errors that a retry cannot fix as non-retryable; everything
// else, including transport failures, stays retryable by the Retry Policy.
func classify(err error) error {
	var invalid order.InvalidTransition
	switch {
	case err == nil:
		return nil
	case errors.Is(err, application.ErrInvalidInput):
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeInvalidInput, err)
	case errors.Is(err, order.ErrOrderNotFound):
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeOrderNotFound, err)
	case errors.As(err, &invalid):
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeInvalidTransition, err)
	default:
		return err
	}
}
