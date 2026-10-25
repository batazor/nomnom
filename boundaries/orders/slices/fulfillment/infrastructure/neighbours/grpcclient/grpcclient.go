// Package grpcclient implements the Activity client ports of fulfillment on the
// command gRPC APIs of payments, restaurants and delivery, called through the
// internal gateway. Every command is addressed by order_id, so orders keeps no
// identifiers of neighbouring contexts.
package grpcclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nomnom/orders/slices/fulfillment/applications/neighbours"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"nomnom/orders/pkg/application"
	deliveryv1 "nomnom/orders/pkg/gateway/gen/nomnom/delivery/v1"
	paymentsv1 "nomnom/orders/pkg/gateway/gen/nomnom/payments/v1"
	restaurantsv1 "nomnom/orders/pkg/gateway/gen/nomnom/restaurants/v1"
)

// ErrOperationPending is retryable: the Activity repeats the same idempotency key
// until the provider answers.
var ErrOperationPending = errors.New("payment operation is pending")

// Payments implements neighbours.Payments. Every operation first resolves the
// Payment of the Order by the idempotent CreatePayment.
type Payments struct {
	client paymentsv1.PaymentsServiceClient
}

func NewPayments(conn grpc.ClientConnInterface) *Payments {
	return &Payments{client: paymentsv1.NewPaymentsServiceClient(conn)}
}

var _ neighbours.Payments = (*Payments)(nil)

func (p *Payments) Authorize(ctx context.Context, r neighbours.PaymentRequest) (neighbours.PaymentDecision, error) {
	id, err := p.payment(ctx, r)
	if err != nil {
		return neighbours.PaymentDecision{}, err
	}
	resp, err := p.client.AuthorizePayment(ctx, &paymentsv1.AuthorizePaymentRequest{PaymentId: id, IdempotencyKey: r.IdempotencyKey})
	if err != nil {
		return neighbours.PaymentDecision{}, mapError("payments.AuthorizePayment", err)
	}
	return decision("payments.AuthorizePayment", resp.GetOperation())
}

func (p *Payments) Capture(ctx context.Context, r neighbours.PaymentRequest) (neighbours.PaymentDecision, error) {
	id, err := p.payment(ctx, r)
	if err != nil {
		return neighbours.PaymentDecision{}, err
	}
	resp, err := p.client.CapturePayment(ctx, &paymentsv1.CapturePaymentRequest{PaymentId: id, IdempotencyKey: r.IdempotencyKey})
	if err != nil {
		return neighbours.PaymentDecision{}, mapError("payments.CapturePayment", err)
	}
	return decision("payments.CapturePayment", resp.GetOperation())
}

// CancelAuthorization and Refund are compensations: a decline is not a branch
// of the Saga, so it stays a retryable error visible to operators.
func (p *Payments) CancelAuthorization(ctx context.Context, r neighbours.PaymentRequest) error {
	id, err := p.payment(ctx, r)
	if err != nil {
		return err
	}
	resp, err := p.client.CancelAuthorization(ctx, &paymentsv1.CancelAuthorizationRequest{PaymentId: id, IdempotencyKey: r.IdempotencyKey})
	if err != nil {
		return mapError("payments.CancelAuthorization", err)
	}
	return compensated("payments.CancelAuthorization", resp.GetOperation())
}

func (p *Payments) Refund(ctx context.Context, r neighbours.PaymentRequest) error {
	id, err := p.payment(ctx, r)
	if err != nil {
		return err
	}
	resp, err := p.client.RefundPayment(ctx, &paymentsv1.RefundPaymentRequest{PaymentId: id, IdempotencyKey: r.IdempotencyKey})
	if err != nil {
		return mapError("payments.RefundPayment", err)
	}
	return compensated("payments.RefundPayment", resp.GetOperation())
}

func (p *Payments) payment(ctx context.Context, r neighbours.PaymentRequest) (string, error) {
	resp, err := p.client.CreatePayment(ctx, &paymentsv1.CreatePaymentRequest{
		OrderId: r.OrderID,
		Amount:  &paymentsv1.Money{Amount: r.Amount, Currency: r.Currency},
	})
	if err != nil {
		return "", mapError("payments.CreatePayment", err)
	}
	return resp.GetPaymentId(), nil
}

func decision(call string, op *paymentsv1.PaymentOperation) (neighbours.PaymentDecision, error) {
	switch op.GetStatus() {
	case paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_SUCCEEDED:
		return neighbours.PaymentDecision{Approved: true}, nil
	case paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_DECLINED:
		return neighbours.PaymentDecision{Reason: op.GetDeclineReason()}, nil
	case paymentsv1.PaymentOperationStatus_PAYMENT_OPERATION_STATUS_PENDING:
		return neighbours.PaymentDecision{}, fmt.Errorf("%s: %w", call, ErrOperationPending)
	default:
		return neighbours.PaymentDecision{}, fmt.Errorf("%s: unexpected operation status %s", call, op.GetStatus())
	}
}

func compensated(call string, op *paymentsv1.PaymentOperation) error {
	d, err := decision(call, op)
	switch {
	case err != nil:
		return err
	case !d.Approved:
		return fmt.Errorf("%s declined: %s", call, d.Reason)
	}
	return nil
}

// Restaurants implements neighbours.Restaurants.
type Restaurants struct {
	client restaurantsv1.RestaurantsServiceClient
}

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1.NewRestaurantsServiceClient(conn)}
}

var _ neighbours.Restaurants = (*Restaurants)(nil)

func (r *Restaurants) RequestOrder(ctx context.Context, request neighbours.RestaurantOrderRequest) error {
	items := make([]*restaurantsv1.OrderRequestItem, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, &restaurantsv1.OrderRequestItem{MenuItemId: item.MenuItemID, Quantity: int32(item.Quantity)}) //nolint:gosec // quantities are small positive values validated by the Order.
	}
	_, err := r.client.ReceiveOrderRequest(ctx, &restaurantsv1.ReceiveOrderRequestRequest{
		RestaurantId: request.RestaurantID,
		OrderId:      request.OrderID,
		Items:        items,
	})
	return mapError("restaurants.ReceiveOrderRequest", err)
}

// CancelOrder treats a rejection by stage as done: the owner has already closed
// the Restaurant Order, so there is nothing left to compensate.
func (r *Restaurants) CancelOrder(ctx context.Context, request neighbours.RestaurantOrderRequest) error {
	resp, err := r.client.CancelRestaurantOrder(ctx, &restaurantsv1.CancelRestaurantOrderRequest{
		RestaurantId: request.RestaurantID,
		OrderId:      request.OrderID,
	})
	if err != nil {
		return mapError("restaurants.CancelRestaurantOrder", err)
	}
	if !resp.GetCancelled() {
		slog.InfoContext(ctx, "restaurant order not cancelled",
			slog.String("order_id", request.OrderID), slog.String("rejection_reason", resp.GetRejectionReason()))
	}
	return nil
}

// Delivery implements neighbours.Delivery; the pickup address is the address
// of the restaurant read from restaurants.
type Delivery struct {
	client      deliveryv1.DeliveryServiceClient
	restaurants restaurantsv1.RestaurantsServiceClient
}

func NewDelivery(conn grpc.ClientConnInterface) *Delivery {
	return &Delivery{
		client:      deliveryv1.NewDeliveryServiceClient(conn),
		restaurants: restaurantsv1.NewRestaurantsServiceClient(conn),
	}
}

var _ neighbours.Delivery = (*Delivery)(nil)

func (d *Delivery) Create(ctx context.Context, request neighbours.DeliveryRequest) error {
	restaurant, err := d.restaurants.GetRestaurant(ctx, &restaurantsv1.GetRestaurantRequest{RestaurantId: request.RestaurantID})
	if err != nil {
		return mapError("restaurants.GetRestaurant", err)
	}
	pickup := restaurant.GetRestaurant().GetAddress()
	if pickup == "" {
		return fmt.Errorf("restaurant %s: %w", request.RestaurantID,
			application.InvalidInput("pickup_address", "restaurant has no address"))
	}
	_, err = d.client.CreateDelivery(ctx, &deliveryv1.CreateDeliveryRequest{
		OrderId:       request.OrderID,
		PickupAddress: pickup,
	})
	return mapError("delivery.CreateDelivery", err)
}

// Replan is best effort: a state precondition (a candidate became busy, the
// courier was assigned meanwhile) is logged, and the next assignment deadline
// of the workflow replans again instead of failing the Order.
func (d *Delivery) Replan(ctx context.Context, request neighbours.DeliveryRequest) error {
	resp, err := d.client.ReplanDelivery(ctx, &deliveryv1.ReplanDeliveryRequest{OrderId: request.OrderID})
	if status.Code(err) == codes.FailedPrecondition {
		slog.InfoContext(ctx, "replan of delivery skipped", slog.String("order_id", request.OrderID), "error", err)
		return nil
	}
	if err != nil {
		return mapError("delivery.ReplanDelivery", err)
	}
	if resp.GetOfferedCourierId() == "" {
		slog.InfoContext(ctx, "replan of delivery found no courier", slog.String("order_id", request.OrderID))
	}
	return nil
}

// Cancel treats a rejection by state as done, like CancelOrder.
func (d *Delivery) Cancel(ctx context.Context, request neighbours.DeliveryRequest) error {
	resp, err := d.client.CancelDelivery(ctx, &deliveryv1.CancelDeliveryRequest{OrderId: request.OrderID})
	if err != nil {
		return mapError("delivery.CancelDelivery", err)
	}
	if !resp.GetCancelled() {
		slog.InfoContext(ctx, "delivery not cancelled",
			slog.String("order_id", request.OrderID), slog.String("rejection_reason", resp.GetRejectionReason()))
	}
	return nil
}

// mapError makes contract violations non-retryable through application.ErrInvalidInput;
// transport and identity failures stay retryable until fixed by deployment.
func mapError(call string, err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.NotFound, codes.FailedPrecondition, codes.AlreadyExists:
		return fmt.Errorf("%s: %w: %w", call, application.ErrInvalidInput, err)
	default:
		return fmt.Errorf("%s: %w", call, err)
	}
}
