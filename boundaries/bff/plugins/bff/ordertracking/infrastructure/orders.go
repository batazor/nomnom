package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ordersv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/orders/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// Orders implements applications.Orders on orders.v1.OrdersService.
type Orders struct {
	client ordersv1.OrdersServiceClient
}

func NewOrders(conn grpc.ClientConnInterface) *Orders {
	return &Orders{client: ordersv1.NewOrdersServiceClient(conn)}
}

var orderStates = map[ordersv1.OrderState]string{
	ordersv1.OrderState_ORDER_STATE_PLACED:    "placed",
	ordersv1.OrderState_ORDER_STATE_CONFIRMED: "confirmed",
	ordersv1.OrderState_ORDER_STATE_CANCELLED: "cancelled",
	ordersv1.OrderState_ORDER_STATE_COMPLETED: "completed",
}

func (o *Orders) GetOrder(ctx context.Context, orderID string) (applications.OrderSnapshot, error) {
	response, err := o.client.GetOrder(ctx, &ordersv1.GetOrderRequest{OrderId: orderID})
	if err != nil {
		return applications.OrderSnapshot{}, ordersError("get order", err)
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
		return "", ordersError("request order cancellation", err)
	}
	switch response.GetOutcome() {
	case ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_REQUESTED:
		return applications.OutcomeRequested, nil
	case ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_ALREADY_CANCELLED:
		return applications.OutcomeAlreadyCancelled, nil
	}
	return "", fmt.Errorf("request order cancellation: unknown outcome %s", response.GetOutcome())
}

// ordersError hides whether a foreign Order exists and turns FAILED_PRECONDITION
// into a business refusal named by its ErrorInfo reason.
func ordersError(operation string, err error) error {
	st := status.Convert(err)
	switch st.Code() {
	case codes.NotFound, codes.PermissionDenied:
		return applications.ErrOrderNotFound
	case codes.FailedPrecondition:
		for _, detail := range st.Details() {
			if info, ok := detail.(*errdetails.ErrorInfo); ok {
				return applications.CancellationRefused{Reason: info.GetReason()}
			}
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func snapshot(order *ordersv1.Order) applications.OrderSnapshot {
	result := applications.OrderSnapshot{
		ID:              order.GetOrderId(),
		RestaurantID:    order.GetRestaurantId(),
		State:           orderStates[order.GetState()],
		Total:           money(order.GetTotal()),
		DeliveryAddress: order.GetDeliveryAddress(),
		PlacedAt:        order.GetPlacedAt().AsTime(),
	}
	for _, item := range order.GetItems() {
		result.Items = append(result.Items, applications.OrderLine{
			MenuItemID: item.GetMenuItemId(),
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
	if window := order.GetCancellationWindow(); window != nil {
		result.Window = &applications.CancellationWindow{
			OpensAt:  window.GetOpensAt().AsTime(),
			ClosesAt: window.GetClosesAt().AsTime(),
		}
	}
	return result
}

func money(value *ordersv1.Money) applications.Money {
	return applications.Money{Amount: value.GetAmount(), Currency: value.GetCurrency()}
}
