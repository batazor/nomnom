// Package grpcadapter maps the public nomnom.orders.v1 transport contract to
// application commands and queries. Protobuf types do not cross this package.
package grpcadapter

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/closure/requestordercancellation"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
	"nomnom/orders/slices/simulator/applications/switchsimulator"
)

// SimulatorSwitch is the inbound port of GetSimulator and SetSimulatorEnabled.
type SimulatorSwitch interface {
	Get(ctx context.Context, query switchsimulator.GetQuery) (bool, error)
	Set(ctx context.Context, command switchsimulator.SetCommand) (bool, error)
}

// OrderHandlers are the order use cases the API serves. The composition root
// fills them from the assembled order slice.
type OrderHandlers struct {
	PlaceOrder               usecase.Handler[placeorder.Input, placeorder.Result]
	RequestOrderCancellation usecase.Handler[requestordercancellation.Input, requestordercancellation.Result]
	GetOrder                 usecase.Handler[getorder.Query, orderdetails.View]
	ListCustomerOrders       usecase.Handler[listcustomerorders.Query, listcustomerorders.Page]
}

type Service struct {
	ordersv1.UnimplementedOrdersServiceServer
	module    OrderHandlers
	simulator SimulatorSwitch
	subjects  SubjectResolver
}

func NewService(module OrderHandlers, simulator SimulatorSwitch, subjects SubjectResolver) *Service {
	return &Service{module: module, simulator: simulator, subjects: subjects}
}

func (s *Service) GetSimulator(ctx context.Context, _ *ordersv1.GetSimulatorRequest) (*ordersv1.GetSimulatorResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	enabled, err := s.simulator.Get(ctx, switchsimulator.GetQuery{Subject: subject})
	if err != nil {
		return nil, transportError(err)
	}
	return &ordersv1.GetSimulatorResponse{Enabled: enabled}, nil
}

func (s *Service) SetSimulatorEnabled(ctx context.Context, request *ordersv1.SetSimulatorEnabledRequest) (*ordersv1.SetSimulatorEnabledResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	enabled, err := s.simulator.Set(ctx, switchsimulator.SetCommand{Subject: subject, Enabled: request.GetEnabled()})
	if err != nil {
		return nil, transportError(err)
	}
	return &ordersv1.SetSimulatorEnabledResponse{Enabled: enabled}, nil
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
		MarketID:   request.GetMarketId(),
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
// Customer; a service subject gets PERMISSION_DENIED from the use case.
func (s *Service) PlaceOrder(ctx context.Context, request *ordersv1.PlaceOrderRequest) (*ordersv1.PlaceOrderResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	items := make([]placeorder.ItemInput, 0, len(request.GetItems()))
	for _, item := range request.GetItems() {
		items = append(items, placeorder.ItemInput{MenuItemID: order.MenuItemID(item.GetMenuItemId()), Quantity: int(item.GetQuantity())})
	}
	var windowStart time.Time
	if request.GetDeliveryWindowStart() != nil {
		if err := request.GetDeliveryWindowStart().CheckValid(); err != nil {
			return nil, transportError(usecase.InvalidInput("delivery_window_start", "is not a valid timestamp"))
		}
		windowStart = request.GetDeliveryWindowStart().AsTime()
	}
	result, err := s.module.PlaceOrder.Handle(ctx, placeorder.Input{
		Subject:             subject,
		IdempotencyKey:      request.GetIdempotencyKey(),
		RestaurantID:        order.RestaurantID(request.GetRestaurantId()),
		Items:               items,
		Priority:            placementPriority(request.GetPriority()),
		DeliveryWindowStart: windowStart,
	})
	if err != nil {
		return nil, transportError(err)
	}
	return &ordersv1.PlaceOrderResponse{
		OrderId:  string(result.OrderID),
		Replayed: result.Replayed,
	}, nil
}

// priorityOf maps the API priority to Place Order (FR-650): UNSPECIFIED is
// the empty priority, which Place Order takes as STANDARD.
var priorityOf = map[ordersv1.OrderPriority]string{
	ordersv1.OrderPriority_ORDER_PRIORITY_UNSPECIFIED: "",
	ordersv1.OrderPriority_ORDER_PRIORITY_STANDARD:    string(order.PriorityStandard),
	ordersv1.OrderPriority_ORDER_PRIORITY_PREMIUM:     string(order.PriorityPremium),
}

// placementPriority passes an unknown enum value on by its number, so Place
// Order refuses it as invalid input instead of taking it as STANDARD.
func placementPriority(priority ordersv1.OrderPriority) string {
	if mapped, known := priorityOf[priority]; known {
		return mapped
	}
	return priority.String()
}

// orderPriority is the API form of a stored priority; the domain knows only
// these two.
var orderPriority = map[order.Priority]ordersv1.OrderPriority{
	order.PriorityStandard: ordersv1.OrderPriority_ORDER_PRIORITY_STANDARD,
	order.PriorityPremium:  ordersv1.OrderPriority_ORDER_PRIORITY_PREMIUM,
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

func orderView(view orderdetails.View) *ordersv1.Order {
	result := &ordersv1.Order{
		OrderId:      string(view.OrderID),
		CustomerId:   string(view.CustomerID),
		RestaurantId: string(view.RestaurantID),
		MarketId:     view.MarketID.String(),
		Items:        make([]*ordersv1.OrderItem, 0, len(view.Items)),
		Total:        money(view.Total),
		Priority:     orderPriority[view.Service.Priority()],
		State:        orderState(view.State),
		PlacedAt:     timestamppb.New(view.PlacedAt),
		Transitions:  make([]*ordersv1.OrderTransition, 0, len(view.Transitions)),
	}
	for _, item := range view.Items {
		result.Items = append(result.Items, &ordersv1.OrderItem{
			MenuItemId: string(item.MenuItemID),
			Name:       item.Name,
			Quantity:   int32(item.Quantity),
			UnitPrice:  money(item.UnitPrice),
		})
	}
	for _, applied := range view.Transitions {
		result.Transitions = append(result.Transitions, &ordersv1.OrderTransition{
			Trigger:    string(applied.Trigger),
			OccurredAt: timestamppb.New(applied.OccurredAt),
		})
	}
	if !view.CourierAssignedAt.IsZero() {
		result.CourierAssignedAt = timestamppb.New(view.CourierAssignedAt)
	}
	if view.Cancellation != nil {
		result.Cancellation = &ordersv1.Cancellation{
			Outcome:     string(view.Cancellation.Outcome),
			ReasonCode:  view.Cancellation.ReasonCode,
			CancelledAt: timestamppb.New(view.Cancellation.CancelledAt),
		}
	}
	if fee, premium := view.Service.PremiumFee(); premium {
		result.PremiumFee = money(fee)
	}
	result.ExpectedReadyAt = expectedTime(view.Expected.ReadyAt)
	result.ExpectedPickupAt = expectedTime(view.Expected.PickupAt)
	result.ExpectedDeliveredAt = expectedTime(view.Expected.DeliveredAt)
	if !view.DeliveryWindow.IsZero() {
		result.DeliveryWindow = &ordersv1.DeliveryWindow{
			Start: timestamppb.New(view.DeliveryWindow.Start()),
			End:   timestamppb.New(view.DeliveryWindow.End()),
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

// expectedTime leaves a deadline that is not named empty.
func expectedTime(at time.Time) *timestamppb.Timestamp {
	if at.IsZero() {
		return nil
	}
	return timestamppb.New(at)
}

func orderSummary(entry listcustomerorders.Entry) *ordersv1.OrderSummary {
	return &ordersv1.OrderSummary{
		OrderId:      string(entry.OrderID),
		RestaurantId: string(entry.RestaurantID),
		State:        orderState(entry.State),
		Total:        money(entry.Total),
		PlacedAt:     timestamppb.New(entry.PlacedAt),
		Cursor:       entry.Cursor,
		Order:        orderView(entry.View),
	}
}

func money(value vo.Money) *ordersv1.Money {
	return &ordersv1.Money{Amount: value.Amount().String(), Currency: value.Currency()}
}

func orderState(state order.State) ordersv1.OrderState {
	switch state {
	case order.StatePlaced:
		return ordersv1.OrderState_ORDER_STATE_PLACED
	case order.StatePaid:
		return ordersv1.OrderState_ORDER_STATE_PAID
	case order.StateConfirmed:
		return ordersv1.OrderState_ORDER_STATE_CONFIRMED
	case order.StatePreparing:
		return ordersv1.OrderState_ORDER_STATE_PREPARING
	case order.StateReady:
		return ordersv1.OrderState_ORDER_STATE_READY
	case order.StatePickedUp:
		return ordersv1.OrderState_ORDER_STATE_PICKED_UP
	case order.StateRejected:
		return ordersv1.OrderState_ORDER_STATE_REJECTED
	case order.StateDeliveryFailed:
		return ordersv1.OrderState_ORDER_STATE_DELIVERY_FAILED
	case order.StateDelivered:
		return ordersv1.OrderState_ORDER_STATE_DELIVERED
	case order.StateCancelled:
		return ordersv1.OrderState_ORDER_STATE_CANCELLED
	case order.StateCompleted:
		return ordersv1.OrderState_ORDER_STATE_COMPLETED
	default:
		return ordersv1.OrderState_ORDER_STATE_UNSPECIFIED
	}
}
