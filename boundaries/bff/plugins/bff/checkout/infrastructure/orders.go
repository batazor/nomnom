// Package infrastructure adapts the checkout port to the PlaceOrder command of
// orders.v1.OrdersService, called through the internal gateway on behalf of
// the customer (ADR 32).
package infrastructure

import (
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	ordersv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/orders/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpcerr"
)

// Orders implements applications.OrderPlacer.
type Orders struct {
	client ordersv1.OrdersServiceClient
}

func NewOrders(conn grpc.ClientConnInterface) *Orders {
	return &Orders{client: ordersv1.NewOrdersServiceClient(conn)}
}

var _ applications.OrderPlacer = (*Orders)(nil)

var priorities = map[applications.Priority]ordersv1.OrderPriority{
	applications.PriorityStandard: ordersv1.OrderPriority_ORDER_PRIORITY_STANDARD,
	applications.PriorityPremium:  ordersv1.OrderPriority_ORDER_PRIORITY_PREMIUM,
}

func (o *Orders) PlaceOrder(ctx context.Context, input applications.Input) (string, error) {
	priority, ok := priorities[input.Priority]
	if !ok {
		return "", fmt.Errorf("%w: priority %q", applications.ErrInvalidOrder, input.Priority)
	}
	request := &ordersv1.PlaceOrderRequest{
		IdempotencyKey: input.IdempotencyKey,
		RestaurantId:   input.RestaurantID,
		Priority:       priority,
	}
	for _, line := range input.Lines {
		request.Items = append(request.Items, &ordersv1.PlaceOrderItem{
			MenuItemId: line.MenuItemID,
			Quantity:   int32(line.Quantity), // validated by the resolver as a GraphQL Int
		})
	}
	response, err := o.client.PlaceOrder(ctx, request)
	if err != nil {
		return "", placeOrderError(err)
	}
	return response.GetOrderId(), nil
}

// placeOrderError names the refusals of the orders error table:
// FAILED_PRECONDITION is a business refusal, NOT_FOUND an unknown restaurant
// and INVALID_ARGUMENT a malformed order.
func placeOrderError(err error) error {
	reason, metadata, hasInfo := grpcerr.Info(err)
	switch status.Code(err) {
	case codes.FailedPrecondition:
		if hasInfo {
			return applications.PlacementRefused{
				Reason:      reason,
				Codes:       grpcerr.List(metadata["codes"]),
				MenuItemIDs: grpcerr.List(metadata["menu_item_ids"]),
			}
		}
	case codes.NotFound:
		if reason == "RESTAURANT_NOT_FOUND" {
			return applications.ErrRestaurantNotFound
		}
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", applications.ErrInvalidOrder, status.Convert(err).Message())
	}
	return fmt.Errorf("place order: %w", err)
}
