package main

import (
	"context"
	"errors"
	"log"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/infrastructure"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/gateway"
)

type placeOrderHandler interface {
	Handle(ctx context.Context, input applications.Input) (applications.Result, error)
}

// checkout holds the scenario of the checkout screen.
type checkout struct {
	placeOrder placeOrderHandler
}

// newCheckout connects the slice to orders; the payload reuses the GetOrder
// scenario of order tracking, so without it checkout is not available either.
func newCheckout(customers *identity.Identity, getenv func(string) string, tracking *orderTracking) (*checkout, error) {
	address := getenv(envInternalGateway)
	if customers == nil || address == "" || tracking == nil {
		return nil, nil
	}
	conn, err := gateway.Dial(address, "nomnom.orders", customers.Outgoing)
	if err != nil {
		return nil, err
	}
	return &checkout{
		placeOrder: applications.NewPlaceOrder(infrastructure.NewOrders(conn), tracking.getOrder),
	}, nil
}

func loadCheckout(customers *identity.Identity, tracking *orderTracking) *checkout {
	checkout, err := newCheckout(customers, os.Getenv, tracking)
	if err != nil {
		log.Fatalf("connect checkout: %v", err)
	}
	return checkout
}

// MutationPlaceOrder places the Order on behalf of the verified customer;
// business refusals are payload problems, not GraphQL errors (FR-500).
func (s *BffService) MutationPlaceOrder(ctx context.Context, request *service.MutationPlaceOrderRequest) (*service.MutationPlaceOrderResponse, error) {
	input := request.GetInput()
	if input.GetRestaurantId() == "" || input.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "restaurantId and idempotencyKey are required")
	}
	ctx, err := s.verifiedCustomer(ctx, s.checkout != nil)
	if err != nil {
		return nil, err
	}
	lines := make([]applications.Line, 0, len(input.GetItems()))
	for _, item := range input.GetItems() {
		lines = append(lines, applications.Line{MenuItemID: item.GetMenuItemId(), Quantity: int(item.GetQuantity())})
	}
	result, err := s.checkout.placeOrder.Handle(ctx, applications.Input{
		RestaurantID:    input.GetRestaurantId(),
		Lines:           lines,
		DeliveryAddress: input.GetDeliveryAddress(),
		IdempotencyKey:  input.GetIdempotencyKey(),
	})
	switch {
	case errors.Is(err, applications.ErrRestaurantNotFound):
		// The schema has no problem for it: the client offered a restaurant
		// that does not exist, which is not a fixable cart problem.
		return nil, status.Error(codes.NotFound, "restaurant not found")
	case errors.Is(err, applications.ErrInvalidOrder):
		return nil, status.Error(codes.InvalidArgument, "order input is invalid")
	case err != nil:
		return nil, internalError("placeOrder", err)
	}
	payload := &service.PlaceOrderPayload{Problems: make([]*service.PlaceOrderProblem, 0, len(result.Problems))}
	for _, problem := range result.Problems {
		payload.Problems = append(payload.Problems, &service.PlaceOrderProblem{
			Code:        service.PlaceOrderProblemCode(service.PlaceOrderProblemCode_value["PLACE_ORDER_PROBLEM_CODE_"+string(problem.Code)]),
			MenuItemIds: problem.MenuItemIDs,
		})
	}
	if result.Order != nil {
		payload.Order = orderMessage(*result.Order)
	}
	return &service.MutationPlaceOrderResponse{PlaceOrder: payload}, nil
}
