// Package grpcadapter maps the public nomnom.orders.v1 transport contract to
// application commands and queries. Protobuf types do not cross this package.
package grpcadapter

import (
	"context"
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

const errorDomain = "nomnom.orders.v1"

type Service struct {
	ordersv1.UnimplementedOrdersServiceServer
	module   *orderdi.Module
	subjects SubjectResolver
}

func NewService(module *orderdi.Module, subjects SubjectResolver) *Service {
	return &Service{module: module, subjects: subjects}
}

func (s *Service) GetOrder(ctx context.Context, request *ordersv1.GetOrderRequest) (*ordersv1.GetOrderResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	view, err := s.module.GetOrder.Handle(ctx, getorder.Query{
		Subject: subject,
		OrderID: order.ID(request.GetOrderId()),
	})
	if err != nil {
		return nil, transportError(err)
	}
	return &ordersv1.GetOrderResponse{Order: orderView(view)}, nil
}

func (s *Service) ListCustomerOrders(ctx context.Context, request *ordersv1.ListCustomerOrdersRequest) (*ordersv1.ListCustomerOrdersResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	page, err := s.module.ListCustomerOrders.Handle(ctx, listcustomerorders.Query{
		Subject:    subject,
		CustomerID: order.CustomerID(request.GetCustomerId()),
		Limit:      int(request.GetPageSize()),
		Cursor:     request.GetPageToken(),
	})
	if err != nil {
		return nil, transportError(err)
	}
	response := &ordersv1.ListCustomerOrdersResponse{
		Orders:        make([]*ordersv1.OrderSummary, 0, len(page.Orders)),
		NextPageToken: page.NextCursor,
	}
	for _, summary := range page.Orders {
		response.Orders = append(response.Orders, orderSummary(summary))
	}
	return response, nil
}

func orderView(view getorder.OrderView) *ordersv1.Order {
	result := &ordersv1.Order{
		OrderId:          string(view.OrderID),
		CustomerId:       string(view.CustomerID),
		RestaurantId:     string(view.RestaurantID),
		Items:            make([]*ordersv1.OrderItem, 0, len(view.Items)),
		DeliveryAddress:  view.DeliveryAddress,
		Total:            money(view.Total),
		State:            orderState(view.State),
		PlacedAt:         timestamppb.New(view.PlacedAt),
		FulfillmentFacts: make([]*ordersv1.FulfillmentFact, 0, len(view.Facts)),
	}
	for _, item := range view.Items {
		result.Items = append(result.Items, &ordersv1.OrderItem{
			MenuItemId: string(item.MenuItemID),
			Quantity:   int32(item.Quantity),
			UnitPrice:  money(item.UnitPrice),
		})
	}
	for _, fact := range view.Facts {
		result.FulfillmentFacts = append(result.FulfillmentFacts, &ordersv1.FulfillmentFact{
			Kind:       string(fact.Fact),
			OccurredAt: timestamppb.New(fact.OccurredAt),
			Reason:     fact.Reason,
		})
	}
	if view.Cancellation != nil {
		result.Cancellation = &ordersv1.Cancellation{
			Outcome:     string(view.Cancellation.Outcome),
			ReasonCode:  view.Cancellation.ReasonCode,
			CancelledAt: timestamppb.New(view.Cancellation.CancelledAt),
		}
	}
	if view.CancellationWindow != nil {
		result.CancellationWindow = &ordersv1.CancellationWindow{
			OpensAt:  timestamppb.New(view.CancellationWindow.OpensAt),
			ClosesAt: timestamppb.New(view.CancellationWindow.ClosesAt),
		}
	}
	return result
}

func orderSummary(summary listcustomerorders.OrderSummary) *ordersv1.OrderSummary {
	return &ordersv1.OrderSummary{
		OrderId:      string(summary.OrderID),
		RestaurantId: string(summary.RestaurantID),
		State:        orderState(summary.State),
		Total:        money(summary.Total),
		PlacedAt:     timestamppb.New(summary.PlacedAt),
	}
}

func money(value vo.Money) *ordersv1.Money {
	return &ordersv1.Money{Amount: value.Amount().String(), Currency: value.Currency()}
}

func orderState(state order.State) ordersv1.OrderState {
	switch state {
	case order.StatePlaced:
		return ordersv1.OrderState_ORDER_STATE_PLACED
	case order.StateConfirmed:
		return ordersv1.OrderState_ORDER_STATE_CONFIRMED
	case order.StateCancelled:
		return ordersv1.OrderState_ORDER_STATE_CANCELLED
	case order.StateCompleted:
		return ordersv1.OrderState_ORDER_STATE_COMPLETED
	default:
		return ordersv1.OrderState_ORDER_STATE_UNSPECIFIED
	}
}

func transportError(err error) error {
	code, reason, message := codes.Internal, "INTERNAL_ERROR", "internal error"
	switch {
	case errors.Is(err, ErrUnauthenticated):
		code, reason, message = codes.Unauthenticated, "AUTHENTICATION_REQUIRED", err.Error()
	case errors.Is(err, application.ErrInvalidInput):
		code, reason, message = codes.InvalidArgument, "INVALID_ARGUMENT", err.Error()
	case errors.Is(err, application.ErrPermissionDenied):
		code, reason, message = codes.PermissionDenied, "OPERATION_NOT_ALLOWED", err.Error()
	case errors.Is(err, order.ErrOrderNotFound):
		code, reason, message = codes.NotFound, "ORDER_NOT_FOUND", err.Error()
	case errors.Is(err, order.ErrOrderAlreadyExists):
		code, reason, message = codes.AlreadyExists, "ORDER_ALREADY_EXISTS", err.Error()
	case errors.Is(err, application.ErrConcurrencyConflict):
		code, reason, message = codes.Aborted, "CONCURRENCY_CONFLICT", err.Error()
	}

	withDetails, detailsErr := status.New(code, message).WithDetails(&errdetails.ErrorInfo{
		Reason: reason,
		Domain: errorDomain,
	})
	if detailsErr != nil {
		return status.Error(code, message)
	}
	return withDetails.Err()
}
