// Package grpcadapter maps the public nomnom.orders.v1 transport contract to
// application commands and queries. Protobuf types do not cross this package.
package grpcadapter

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/applications/requestordercancellation"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

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

// PlaceOrder places an Order for the verified subject. Only a user subject is a
// Customer; until the BFF user identity exists, service subjects get
// PERMISSION_DENIED from the use case.
func (s *Service) PlaceOrder(ctx context.Context, request *ordersv1.PlaceOrderRequest) (*ordersv1.PlaceOrderResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	items := make([]placeorder.ItemInput, 0, len(request.GetItems()))
	for _, item := range request.GetItems() {
		items = append(items, placeorder.ItemInput{MenuItemID: order.MenuItemID(item.GetMenuItemId()), Quantity: int(item.GetQuantity())})
	}
	result, err := s.module.PlaceOrder.Handle(ctx, placeorder.Input{
		Subject:         subject,
		IdempotencyKey:  request.GetIdempotencyKey(),
		RestaurantID:    order.RestaurantID(request.GetRestaurantId()),
		Items:           items,
		DeliveryAddress: request.GetDeliveryAddress(),
	})
	if err != nil {
		return nil, transportError(err)
	}
	return &ordersv1.PlaceOrderResponse{
		OrderId:            string(result.OrderID),
		Replayed:           result.Replayed,
		FulfillmentStarted: result.FulfillmentStarted,
	}, nil
}

func (s *Service) RequestOrderCancellation(ctx context.Context, request *ordersv1.RequestOrderCancellationRequest) (*ordersv1.RequestOrderCancellationResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	result, err := s.module.RequestOrderCancellation.Handle(ctx, requestordercancellation.Input{
		Subject:   subject,
		OrderID:   order.ID(request.GetOrderId()),
		RequestID: request.GetRequestId(),
		Reason:    request.GetReason(),
	})
	if err != nil {
		return nil, transportError(err)
	}
	return &ordersv1.RequestOrderCancellationResponse{Outcome: cancellationOutcome(result.Outcome)}, nil
}

func cancellationOutcome(outcome requestordercancellation.Outcome) ordersv1.CancellationRequestOutcome {
	switch outcome {
	case requestordercancellation.OutcomeRequested:
		return ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_REQUESTED
	case requestordercancellation.OutcomeAlreadyCancelled:
		return ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_ALREADY_CANCELLED
	default:
		return ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_UNSPECIFIED
	}
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
		Cursor:       summary.Cursor,
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
