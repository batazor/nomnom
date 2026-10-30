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

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/fulfillment/applications/neighbours"

	"buf.build/gen/go/nomnom/delivery/grpc/go/nomnom/delivery/v1/deliveryv1grpc"
	deliveryv1 "buf.build/gen/go/nomnom/delivery/protocolbuffers/go/nomnom/delivery/v1"
	"buf.build/gen/go/nomnom/payments/grpc/go/nomnom/payments/v1/paymentsv1grpc"
	paymentsv1 "buf.build/gen/go/nomnom/payments/protocolbuffers/go/nomnom/payments/v1"
	"buf.build/gen/go/nomnom/restaurants/grpc/go/nomnom/restaurants/v1/restaurantsv1grpc"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
)

// ErrOperationPending is retryable: the Activity repeats the same idempotency key
// until the provider answers.
var ErrOperationPending = errors.New("payment operation is pending")

// Payments implements neighbours.Payments. Every operation first resolves the
// Payment of the Order by the idempotent CreatePayment.
type Payments struct {
	client paymentsv1grpc.PaymentsServiceClient
}

func NewPayments(conn grpc.ClientConnInterface) *Payments {
	return &Payments{client: paymentsv1grpc.NewPaymentsServiceClient(conn)}
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
		OrderId:  r.OrderID,
		Amount:   &paymentsv1.Money{Amount: r.Amount, Currency: r.Currency},
		MarketId: r.MarketID,
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
	client restaurantsv1grpc.RestaurantsServiceClient
}

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1grpc.NewRestaurantsServiceClient(conn)}
}

var _ neighbours.Restaurants = (*Restaurants)(nil)

func (r *Restaurants) RequestOrder(ctx context.Context, request neighbours.RestaurantOrderRequest) (string, error) {
	items := make([]*restaurantsv1.OrderRequestItem, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, &restaurantsv1.OrderRequestItem{MenuItemId: item.MenuItemID, Quantity: int32(item.Quantity)}) //nolint:gosec // quantities are small positive values validated by the Order.
	}
	priority, known := restaurantPriority[request.Priority]
	if !known {
		return "", fmt.Errorf("%w: order %s has priority %q", usecase.ErrInvalidInput, request.OrderID, request.Priority)
	}
	var schedule *restaurantsv1.OrderSchedule
	if request.Schedule != nil {
		schedule = &restaurantsv1.OrderSchedule{
			WindowStart:    timestamppb.New(request.Schedule.WindowStart),
			WindowEnd:      timestamppb.New(request.Schedule.WindowEnd),
			PlannedReadyAt: timestamppb.New(request.Schedule.PlannedReadyAt),
		}
	}
	resp, err := r.client.ReceiveOrderRequest(ctx, &restaurantsv1.ReceiveOrderRequestRequest{
		RestaurantId: request.RestaurantID,
		OrderId:      request.OrderID,
		Items:        items,
		Priority:     priority,
		Schedule:     schedule,
	})
	if err != nil {
		return "", mapError("restaurants.ReceiveOrderRequest", err)
	}
	if resp.GetRestaurantOrderId() == "" {
		return "", fmt.Errorf("restaurants.ReceiveOrderRequest: order %s: no restaurant_order_id", request.OrderID)
	}
	return resp.GetRestaurantOrderId(), nil
}

// QueueOrder moves an accepted Scheduled Order to the Kitchen Queue; a repeat
// for a QUEUED order succeeds without changes.
func (r *Restaurants) QueueOrder(ctx context.Context, request neighbours.QueueRequest) error {
	_, err := r.client.QueueRestaurantOrder(ctx, &restaurantsv1.QueueRestaurantOrderRequest{RestaurantOrderId: request.RestaurantOrderID})
	return mapError("restaurants.QueueRestaurantOrder", err)
}

// RecordPickup takes the order off the kitchen board; a repeat succeeds without
// changes. NotFound and FailedPrecondition mean the kitchen no longer holds it as
// ready: there is nothing to take off, and the Order goes on.
func (r *Restaurants) RecordPickup(ctx context.Context, request neighbours.RestaurantPickupRequest) error {
	_, err := r.client.RecordRestaurantOrderPickup(ctx, &restaurantsv1.RecordRestaurantOrderPickupRequest{
		OrderId:    request.OrderID,
		PickedUpAt: timestamppb.New(request.PickedUpAt),
	})
	switch status.Code(err) {
	case codes.NotFound, codes.FailedPrecondition:
		return nil
	}
	return mapError("restaurants.RecordRestaurantOrderPickup", err)
}

// ConfirmSlot confirms the held reservation; a window that filled up after the
// hold expired is the decision not confirmed.
func (r *Restaurants) ConfirmSlot(ctx context.Context, request neighbours.SlotRequest) (neighbours.SlotDecision, error) {
	resp, err := r.client.ConfirmScheduledSlot(ctx, &restaurantsv1.ConfirmScheduledSlotRequest{OrderId: request.OrderID})
	if err != nil {
		return neighbours.SlotDecision{}, mapError("restaurants.ConfirmScheduledSlot", err)
	}
	switch resp.GetResult().(type) {
	case *restaurantsv1.ConfirmScheduledSlotResponse_Confirmed:
		return neighbours.SlotDecision{Confirmed: true}, nil
	case *restaurantsv1.ConfirmScheduledSlotResponse_SlotUnavailable:
		return neighbours.SlotDecision{}, nil
	default:
		return neighbours.SlotDecision{}, fmt.Errorf("restaurants.ConfirmScheduledSlot: order %s: no result", request.OrderID)
	}
}

// ReleaseSlot removes the reservation of the Order; a missing one is a no-op
// of restaurants.
func (r *Restaurants) ReleaseSlot(ctx context.Context, request neighbours.SlotRequest) error {
	_, err := r.client.ReleaseScheduledSlot(ctx, &restaurantsv1.ReleaseScheduledSlotRequest{OrderId: request.OrderID})
	return mapError("restaurants.ReleaseScheduledSlot", err)
}

// restaurantPriority is the restaurants form of the priority of an Order.
var restaurantPriority = map[neighbours.Priority]restaurantsv1.OrderPriority{
	neighbours.PriorityStandard: restaurantsv1.OrderPriority_ORDER_PRIORITY_STANDARD,
	neighbours.PriorityPremium:  restaurantsv1.OrderPriority_ORDER_PRIORITY_PREMIUM,
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

// Delivery implements neighbours.Delivery.
type Delivery struct {
	client deliveryv1grpc.DeliveryServiceClient
}

func NewDelivery(conn grpc.ClientConnInterface) *Delivery {
	return &Delivery{client: deliveryv1grpc.NewDeliveryServiceClient(conn)}
}

var _ neighbours.Delivery = (*Delivery)(nil)

func (d *Delivery) Create(ctx context.Context, request neighbours.DeliveryRequest) error {
	_, err := d.client.CreateDelivery(ctx, &deliveryv1.CreateDeliveryRequest{OrderId: request.OrderID, MarketId: request.MarketID})
	return mapError("delivery.CreateDelivery", err)
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

// mapError makes contract violations non-retryable through usecase.ErrInvalidInput;
// transport and identity failures stay retryable until fixed by deployment.
func mapError(call string, err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.NotFound, codes.FailedPrecondition, codes.AlreadyExists:
		return fmt.Errorf("%s: %w: %w", call, usecase.ErrInvalidInput, err)
	default:
		return fmt.Errorf("%s: %w", call, err)
	}
}
