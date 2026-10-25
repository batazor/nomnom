package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

func (s *BffService) QueryOrder(ctx context.Context, request *service.QueryOrderRequest) (*service.QueryOrderResponse, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	view, err := s.tracking.getOrder.Handle(ctx, request.GetId())
	if errors.Is(err, applications.ErrOrderNotFound) {
		return &service.QueryOrderResponse{}, nil
	}
	if err != nil {
		return nil, internalError("order", err)
	}
	order, err := orderMessage(view)
	if err != nil {
		return nil, internalError("order", err)
	}
	return &service.QueryOrderResponse{Order: order}, nil
}

// LookupOrderById resolves the Order entities of orderChanged events (ADR 36)
// with the subscriber's access token: orders hides foreign orders, so a
// subscriber of a foreign id gets NOT_FOUND instead of order data. A repeated
// Protobuf field cannot hold null, so a missing order fails the whole lookup;
// an event carries one key.
func (s *BffService) LookupOrderById(ctx context.Context, request *service.LookupOrderByIdRequest) (*service.LookupOrderByIdResponse, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	orders := make([]*service.Order, 0, len(request.GetKeys()))
	for _, key := range request.GetKeys() {
		view, err := s.tracking.getOrder.Handle(ctx, key.GetId())
		if errors.Is(err, applications.ErrOrderNotFound) {
			return nil, status.Error(codes.NotFound, "order not found")
		}
		if err != nil {
			return nil, internalError("orderChanged", err)
		}
		order, err := orderMessage(view)
		if err != nil {
			return nil, internalError("orderChanged", err)
		}
		orders = append(orders, order)
	}
	return &service.LookupOrderByIdResponse{Result: orders}, nil
}

func (s *BffService) MutationRequestOrderCancellation(ctx context.Context, request *service.MutationRequestOrderCancellationRequest) (*service.MutationRequestOrderCancellationResponse, error) {
	input := request.GetInput()
	if input.GetOrderId() == "" || input.GetRequestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "orderId and requestId are required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.tracking.requestCancellation.Handle(ctx, applications.CancellationRequest{
		OrderID:   input.GetOrderId(),
		RequestID: input.GetRequestId(),
		Reason:    input.GetReason().GetValue(),
	})
	if errors.Is(err, applications.ErrOrderNotFound) {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if err != nil {
		return nil, internalError("requestOrderCancellation", err)
	}
	payload, err := cancellationPayload(result)
	if err != nil {
		return nil, internalError("requestOrderCancellation", err)
	}
	return &service.MutationRequestOrderCancellationResponse{RequestOrderCancellation: payload}, nil
}

// QueryCustomerOrders pages the history of the verified customer, newest
// first; orders filters it by the customer, so a new customer gets an empty
// page.
func (s *BffService) QueryCustomerOrders(ctx context.Context, request *service.QueryCustomerOrdersRequest) (*service.QueryCustomerOrdersResponse, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	customer, _ := identity.UserFrom(ctx)
	page, err := s.tracking.listCustomerOrders.Handle(ctx, customer.ID, int(request.GetFirst()), request.GetAfter().GetValue())
	switch {
	case errors.Is(err, applications.ErrInvalidPageSize):
		return nil, status.Error(codes.InvalidArgument, "first must be between 1 and 100")
	case errors.Is(err, applications.ErrInvalidCursor):
		return nil, status.Error(codes.InvalidArgument, "after is not a cursor of customerOrders")
	case err != nil:
		return nil, internalError("customerOrders", err)
	}
	connection, err := orderConnection(page)
	if err != nil {
		return nil, internalError("customerOrders", err)
	}
	return &service.QueryCustomerOrdersResponse{CustomerOrders: connection}, nil
}
