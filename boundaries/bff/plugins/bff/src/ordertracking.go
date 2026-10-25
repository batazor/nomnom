package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/gateway"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/infrastructure"
)

// envInternalGateway is the address of the internal gateway (ADR 15) through
// which BFF calls bounded contexts.
const envInternalGateway = "BFF__INTERNAL_GATEWAY_ADDR"

type getOrderHandler interface {
	Handle(ctx context.Context, orderID string) (applications.OrderView, error)
}

type requestCancellationHandler interface {
	Handle(ctx context.Context, request applications.CancellationRequest) (applications.CancellationResult, error)
}

// orderTracking holds the scenarios of the order tracking screen.
type orderTracking struct {
	getOrder            getOrderHandler
	requestCancellation requestCancellationHandler
}

// newOrderTracking connects the slice to orders and restaurants. Without BFF
// identity or a gateway address it returns nil: customer resolvers then answer
// that they are not available, while bffInfo keeps working.
func newOrderTracking(customers *identity.Identity, getenv func(string) string) (*orderTracking, error) {
	address := getenv(envInternalGateway)
	if customers == nil || address == "" {
		log.Printf("%s or BFF identity is not set: customer order operations are unavailable", envInternalGateway)
		return nil, nil
	}
	ordersConn, err := gateway.Dial(address, "nomnom.orders", customers.Outgoing)
	if err != nil {
		return nil, err
	}
	restaurantsConn, err := gateway.Dial(address, "nomnom.restaurants", customers.Outgoing)
	if err != nil {
		return nil, err
	}
	orders := infrastructure.NewOrders(ordersConn)
	return &orderTracking{
		getOrder:            applications.NewGetOrder(orders, infrastructure.NewCatalog(restaurantsConn)),
		requestCancellation: applications.NewRequestCancellation(orders),
	}, nil
}

func loadOrderTracking(customers *identity.Identity) *orderTracking {
	tracking, err := newOrderTracking(customers, os.Getenv)
	if err != nil {
		log.Fatalf("connect order tracking: %v", err)
	}
	return tracking
}

// customer verifies the customer of an order tracking operation.
func (s *BffService) customer(ctx context.Context) (context.Context, error) {
	return s.verifiedCustomer(ctx, s.tracking != nil)
}

// verifiedCustomer verifies the access token the Router forwards and puts the
// customer into the context; a missing or invalid token never reaches a
// context. available reports whether the slice of the operation is connected.
func (s *BffService) verifiedCustomer(ctx context.Context, available bool) (context.Context, error) {
	if s.identity == nil {
		return nil, status.Error(codes.Unauthenticated, "authentication is not configured")
	}
	user, err := s.identity.AccessTokens.FromIncomingContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !available {
		return nil, status.Error(codes.Unavailable, "order operations are not available")
	}
	return identity.WithUser(ctx, user), nil
}

func (s *BffService) QueryOrder(ctx context.Context, request *service.QueryOrderRequest) (*service.QueryOrderResponse, error) {
	ctx, err := s.customer(ctx)
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
	return &service.QueryOrderResponse{Order: orderMessage(view)}, nil
}

func (s *BffService) MutationRequestOrderCancellation(ctx context.Context, request *service.MutationRequestOrderCancellationRequest) (*service.MutationRequestOrderCancellationResponse, error) {
	input := request.GetInput()
	if input.GetOrderId() == "" || input.GetRequestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "orderId and requestId are required")
	}
	ctx, err := s.customer(ctx)
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
	payload := &service.RequestOrderCancellationPayload{}
	if result.Problem != "" {
		payload.Problem = &service.OrderCancellationProblem{
			Code: service.OrderCancellationProblemCode(service.OrderCancellationProblemCode_value["ORDER_CANCELLATION_PROBLEM_CODE_"+string(result.Problem)]),
		}
	} else {
		payload.Outcome = service.OrderCancellationOutcome(service.OrderCancellationOutcome_value["ORDER_CANCELLATION_OUTCOME_"+string(result.Outcome)])
	}
	return &service.MutationRequestOrderCancellationResponse{RequestOrderCancellation: payload}, nil
}

// internalError logs the cause and gives the client no internal details.
func internalError(operation string, err error) error {
	log.Printf("%s failed: %v", operation, err)
	return status.Error(codes.Unavailable, fmt.Sprintf("%s is temporarily unavailable", operation))
}

func orderMessage(view applications.OrderView) *service.Order {
	order := &service.Order{
		Id:     view.ID,
		Status: service.OrderStatus(service.OrderStatus_value["ORDER_STATUS_"+string(view.Status)]),
		Restaurant: &service.Restaurant{
			Id:              view.Restaurant.ID,
			Name:            view.Restaurant.Name,
			Address:         view.Restaurant.Address,
			AcceptingOrders: view.Restaurant.IsOpen,
		},
		Total:           money(view.Total),
		DeliveryAddress: view.DeliveryAddress,
		PlacedAt:        timestamp(view.PlacedAt),
	}
	if view.EndReason != "" {
		order.EndReason = service.OrderEndReason(service.OrderEndReason_value["ORDER_END_REASON_"+string(view.EndReason)])
	}
	for _, item := range view.Items {
		order.Items = append(order.Items, &service.OrderItem{
			MenuItem:  &service.OrderedMenuItem{Id: item.MenuItemID, Name: item.Name},
			Quantity:  int32(item.Quantity),
			UnitPrice: money(item.UnitPrice),
			LineTotal: money(item.LineTotal),
		})
	}
	for _, milestone := range view.Milestones {
		order.Milestones = append(order.Milestones, &service.OrderMilestone{
			Kind:       service.OrderMilestoneKind(service.OrderMilestoneKind_value["ORDER_MILESTONE_KIND_"+string(milestone.Kind)]),
			OccurredAt: timestamp(milestone.OccurredAt),
		})
	}
	if view.AvailableUntil != nil {
		order.Cancellation = &service.OrderCancellationAvailability{AvailableUntil: timestamp(*view.AvailableUntil)}
	}
	return order
}

func money(value applications.Money) *service.Money {
	return &service.Money{Amount: value.Amount, Currency: value.Currency}
}

// timestamp formats moments in RFC 3339, as the public schema requires.
func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
