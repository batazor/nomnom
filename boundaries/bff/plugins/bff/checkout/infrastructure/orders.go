// Package infrastructure adapts the checkout port to the PlaceOrder command of
// orders.v1.OrdersService, called through the internal gateway on behalf of
// the customer (ADR 32).
package infrastructure

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	ordersv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/orders/v1"
)

// Orders implements applications.Orders.
type Orders struct {
	client ordersv1.OrdersServiceClient
}

func NewOrders(conn grpc.ClientConnInterface) *Orders {
	return &Orders{client: ordersv1.NewOrdersServiceClient(conn)}
}

var _ applications.Orders = (*Orders)(nil)

func (o *Orders) PlaceOrder(ctx context.Context, input applications.Input) (string, error) {
	request := &ordersv1.PlaceOrderRequest{
		IdempotencyKey:  input.IdempotencyKey,
		RestaurantId:    input.RestaurantID,
		DeliveryAddress: input.DeliveryAddress,
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
	st := status.Convert(err)
	info := errorInfo(st)
	switch st.Code() {
	case codes.FailedPrecondition:
		if info != nil {
			return applications.PlacementRefused{
				Reason:      info.GetReason(),
				Codes:       split(info.GetMetadata()["codes"]),
				MenuItemIDs: split(info.GetMetadata()["menu_item_ids"]),
			}
		}
	case codes.NotFound:
		if info.GetReason() == "RESTAURANT_NOT_FOUND" {
			return applications.ErrRestaurantNotFound
		}
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", applications.ErrInvalidOrder, st.Message())
	}
	return fmt.Errorf("place order: %w", err)
}

func errorInfo(st *status.Status) *errdetails.ErrorInfo {
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	return nil
}

// split reads a comma-separated metadata value; empty means none.
func split(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}
