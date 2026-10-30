// Package infrastructure adapts the order tracking ports to the gRPC APIs of
// orders and restaurants, called through the internal gateway (ADR 15).
package infrastructure

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/orders/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpcerr"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/market"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// Orders implements applications.OrderReader and
// applications.CancellationRequester on orders.v1.OrdersService.
type Orders struct {
	client ordersv1.OrdersServiceClient
}

func NewOrders(conn grpc.ClientConnInterface) *Orders {
	return &Orders{client: ordersv1.NewOrdersServiceClient(conn)}
}

var (
	_ applications.OrderReader           = (*Orders)(nil)
	_ applications.CancellationRequester = (*Orders)(nil)
	_ applications.OrderHistory          = (*Orders)(nil)
)

var orderStates = map[ordersv1.OrderState]string{
	ordersv1.OrderState_ORDER_STATE_PLACED:          "placed",
	ordersv1.OrderState_ORDER_STATE_PAID:            "paid",
	ordersv1.OrderState_ORDER_STATE_CONFIRMED:       "confirmed",
	ordersv1.OrderState_ORDER_STATE_PREPARING:       "preparing",
	ordersv1.OrderState_ORDER_STATE_READY:           "ready",
	ordersv1.OrderState_ORDER_STATE_PICKED_UP:       "picked_up",
	ordersv1.OrderState_ORDER_STATE_REJECTED:        "rejected",
	ordersv1.OrderState_ORDER_STATE_DELIVERY_FAILED: "delivery_failed",
	ordersv1.OrderState_ORDER_STATE_DELIVERED:       "delivered",
	ordersv1.OrderState_ORDER_STATE_CANCELLED:       "cancelled",
	ordersv1.OrderState_ORDER_STATE_COMPLETED:       "completed",
}

func (o *Orders) GetOrder(ctx context.Context, orderID string) (applications.OrderSnapshot, error) {
	response, err := o.client.GetOrder(ctx, &ordersv1.GetOrderRequest{OrderId: orderID})
	if err != nil {
		return applications.OrderSnapshot{}, getOrderError(err)
	}
	return snapshot(response.GetOrder()), nil
}

func (o *Orders) RequestCancellation(ctx context.Context, request applications.CancellationRequest) (applications.CancellationOutcome, error) {
	response, err := o.client.RequestOrderCancellation(ctx, &ordersv1.RequestOrderCancellationRequest{
		OrderId:   request.OrderID,
		RequestId: request.RequestID,
		Reason:    request.Reason,
	})
	if err != nil {
		return "", requestCancellationError(err)
	}
	switch response.GetOutcome() {
	case ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_REQUESTED:
		return applications.OutcomeRequested, nil
	case ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_ALREADY_CANCELLED:
		return applications.OutcomeAlreadyCancelled, nil
	}
	return "", fmt.Errorf("request order cancellation: unknown outcome %s", response.GetOutcome())
}

// ListCustomerOrders reads the history of customerID, the verified user of
// ctx: orders lets a customer read their own history, within the Market of
// the request.
func (o *Orders) ListCustomerOrders(ctx context.Context, customerID string, size int, cursor string) (applications.HistoryPage, error) {
	marketID, err := market.From(ctx)
	if err != nil {
		return applications.HistoryPage{}, fmt.Errorf("list customer orders: %w", err)
	}
	response, err := o.client.ListCustomerOrders(ctx, &ordersv1.ListCustomerOrdersRequest{
		CustomerId: customerID,
		PageSize:   int32(size),
		PageToken:  cursor,
		MarketId:   marketID,
	})
	if status.Code(err) == codes.InvalidArgument && cursor != "" {
		return applications.HistoryPage{}, applications.ErrInvalidCursor
	}
	if err != nil {
		return applications.HistoryPage{}, fmt.Errorf("list customer orders: %w", err)
	}
	page := applications.HistoryPage{
		Entries:    make([]applications.HistoryEntry, 0, len(response.GetOrders())),
		NextCursor: response.GetNextPageToken(),
	}
	for _, summary := range response.GetOrders() {
		if summary.GetOrder() == nil {
			return applications.HistoryPage{}, fmt.Errorf("list customer orders: order %s without its card", summary.GetOrderId())
		}
		page.Entries = append(page.Entries, applications.HistoryEntry{Order: snapshot(summary.GetOrder()), Cursor: summary.GetCursor()})
	}
	return page, nil
}

// getOrderError hides whether a foreign Order exists. GetOrder has no
// business refusals: any other error, FAILED_PRECONDITION included, stays an
// error of the read.
func getOrderError(err error) error {
	switch status.Code(err) {
	case codes.NotFound, codes.PermissionDenied:
		return applications.ErrOrderNotFound
	}
	return fmt.Errorf("get order: %w", err)
}

// requestCancellationError hides whether a foreign Order exists and turns
// FAILED_PRECONDITION into a business refusal named by its ErrorInfo reason.
func requestCancellationError(err error) error {
	switch status.Code(err) {
	case codes.NotFound, codes.PermissionDenied:
		return applications.ErrOrderNotFound
	case codes.FailedPrecondition:
		if reason, _, ok := grpcerr.Info(err); ok {
			return applications.CancellationRefused{Reason: reason}
		}
	}
	return fmt.Errorf("request order cancellation: %w", err)
}

// orderPriorities has no UNSPECIFIED: orders always names the priority; an
// unknown one stays empty and the presenter refuses it.
var orderPriorities = map[ordersv1.OrderPriority]applications.Priority{
	ordersv1.OrderPriority_ORDER_PRIORITY_STANDARD: applications.PriorityStandard,
	ordersv1.OrderPriority_ORDER_PRIORITY_PREMIUM:  applications.PriorityPremium,
}

func snapshot(order *ordersv1.Order) applications.OrderSnapshot {
	result := applications.OrderSnapshot{
		ID:           order.GetOrderId(),
		RestaurantID: order.GetRestaurantId(),
		State:        orderStates[order.GetState()],
		Total:        money(order.GetTotal()),
		Priority:     orderPriorities[order.GetPriority()],
		PlacedAt:     order.GetPlacedAt().AsTime(),
		Deadlines: applications.Deadlines{
			ExpectedReadyAt:     moment(order.GetExpectedReadyAt()),
			ExpectedPickupAt:    moment(order.GetExpectedPickupAt()),
			ExpectedDeliveredAt: moment(order.GetExpectedDeliveredAt()),
		},
	}
	if fee := order.GetPremiumFee(); fee != nil {
		premium := money(fee)
		result.PremiumFee = &premium
	}
	for _, item := range order.GetItems() {
		result.Items = append(result.Items, applications.OrderLine{
			MenuItemID: item.GetMenuItemId(),
			Name:       item.GetName(),
			Quantity:   int(item.GetQuantity()),
			UnitPrice:  money(item.GetUnitPrice()),
		})
	}
	for _, fact := range order.GetFulfillmentFacts() {
		result.Facts = append(result.Facts, applications.Fact{Kind: fact.GetKind(), OccurredAt: fact.GetOccurredAt().AsTime()})
	}
	if cancellation := order.GetCancellation(); cancellation != nil {
		result.Cancellation = &applications.Cancellation{
			Outcome:     cancellation.GetOutcome(),
			ReasonCode:  cancellation.GetReasonCode(),
			CancelledAt: cancellation.GetCancelledAt().AsTime(),
		}
	}
	if window := order.GetDeliveryWindow(); window != nil {
		result.DeliveryWindow = &applications.DeliveryWindow{
			Start: window.GetStart().AsTime(),
			End:   window.GetEnd().AsTime(),
		}
	}
	if window := order.GetCancellationWindow(); window != nil {
		result.Window = &applications.CancellationWindow{
			OpensAt:  window.GetOpensAt().AsTime(),
			ClosesAt: window.GetClosesAt().AsTime(),
		}
	}
	return result
}

// moment reads an optional deadline: an unset Timestamp is a deadline nobody
// named.
func moment(value *timestamppb.Timestamp) *time.Time {
	if value == nil {
		return nil
	}
	t := value.AsTime()
	return &t
}

func money(value *ordersv1.Money) applications.Money {
	return applications.Money{Amount: value.GetAmount(), Currency: value.GetCurrency()}
}
