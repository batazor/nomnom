// Package infrastructure adapts the kitchen ports to restaurants.v1.RestaurantsService
// through the internal gateway: the kitchen board (ListActiveRestaurantOrders,
// GetKitchenQueue) and the kitchen commands on behalf of the user — service
// token of BFF and user assertion (ADR 32) — and the public GetRestaurant and
// GetMenu without the user. restaurants authorizes restaurant#operate_kitchen.
package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
)

// Restaurants implements the kitchen ports.
type Restaurants struct {
	client restaurantsv1.RestaurantsServiceClient
}

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1.NewRestaurantsServiceClient(conn)}
}

var (
	_ applications.KitchenBoard    = (*Restaurants)(nil)
	_ applications.KitchenCommands = (*Restaurants)(nil)
	_ applications.Catalog         = (*Restaurants)(nil)
)

var stages = map[restaurantsv1.RestaurantOrderState]applications.Stage{
	restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_RECEIVED:  applications.StageReceived,
	restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_QUEUED:    applications.StageQueued,
	restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_PREPARING: applications.StagePreparing,
	restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_ASSEMBLED: applications.StageAssembled,
	restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_READY:     applications.StageReady,
}

// ActiveOrders keeps the user of ctx: the interceptor turns it into the user
// assertion. An order in a state outside the kitchen board is an error: the
// RPC promises only the board states.
func (r *Restaurants) ActiveOrders(ctx context.Context, restaurantID string) ([]applications.ActiveOrder, error) {
	response, err := r.client.ListActiveRestaurantOrders(ctx, &restaurantsv1.ListActiveRestaurantOrdersRequest{RestaurantId: restaurantID})
	if err != nil {
		return nil, boardError("list active restaurant orders", err)
	}
	orders := make([]applications.ActiveOrder, 0, len(response.GetOrders()))
	for _, order := range response.GetOrders() {
		state := order.GetRestaurantOrder().GetState()
		stage, ok := stages[state]
		if !ok {
			return nil, fmt.Errorf("restaurant order %s in unexpected state %s", order.GetRestaurantOrder().GetRestaurantOrderId(), state)
		}
		lines := make([]applications.ActiveOrderLine, 0, len(order.GetItems()))
		for _, item := range order.GetItems() {
			lines = append(lines, applications.ActiveOrderLine{MenuItemID: item.GetMenuItemId(), Quantity: int(item.GetQuantity())})
		}
		active := applications.ActiveOrder{
			ID:      order.GetRestaurantOrder().GetRestaurantOrderId(),
			OrderID: order.GetRestaurantOrder().GetOrderId(),
			Stage:   stage,
			Lines:   lines,
		}
		if order.GetReceivedAt() != nil {
			active.ReceivedAt = order.GetReceivedAt().AsTime()
		}
		orders = append(orders, active)
	}
	return orders, nil
}

func (r *Restaurants) QueueOrder(ctx context.Context, restaurantID string) ([]string, error) {
	response, err := r.client.GetKitchenQueue(ctx, &restaurantsv1.GetKitchenQueueRequest{RestaurantId: restaurantID})
	if err != nil {
		return nil, boardError("get kitchen queue", err)
	}
	ids := make([]string, 0, len(response.GetEntries()))
	for _, entry := range response.GetEntries() {
		ids = append(ids, entry.GetRestaurantOrder().GetRestaurantOrderId())
	}
	return ids, nil
}

// Apply keeps the user of ctx. The reject reason stays empty: the screen has
// no reason field.
func (r *Restaurants) Apply(ctx context.Context, restaurantID, kitchenOrderID string, action applications.Action) error {
	var err error
	switch action {
	case applications.ActionAccept:
		_, err = r.client.AcceptRestaurantOrder(ctx, &restaurantsv1.AcceptRestaurantOrderRequest{RestaurantId: restaurantID, RestaurantOrderId: kitchenOrderID})
	case applications.ActionReject:
		_, err = r.client.RejectRestaurantOrder(ctx, &restaurantsv1.RejectRestaurantOrderRequest{RestaurantId: restaurantID, RestaurantOrderId: kitchenOrderID})
	case applications.ActionStartPreparation:
		_, err = r.client.StartPreparation(ctx, &restaurantsv1.StartPreparationRequest{RestaurantId: restaurantID, RestaurantOrderId: kitchenOrderID})
	case applications.ActionMarkAssembled:
		_, err = r.client.MarkRestaurantOrderAssembled(ctx, &restaurantsv1.MarkRestaurantOrderAssembledRequest{RestaurantId: restaurantID, RestaurantOrderId: kitchenOrderID})
	case applications.ActionMarkReady:
		_, err = r.client.MarkRestaurantOrderReady(ctx, &restaurantsv1.MarkRestaurantOrderReadyRequest{RestaurantId: restaurantID, RestaurantOrderId: kitchenOrderID})
	default:
		return fmt.Errorf("%w %q", applications.ErrUnknownAction, action)
	}
	if err != nil {
		return commandError(string(action), err)
	}
	return nil
}

func (r *Restaurants) Restaurant(ctx context.Context, restaurantID string) (applications.Restaurant, error) {
	response, err := r.client.GetRestaurant(identity.WithoutUser(ctx), &restaurantsv1.GetRestaurantRequest{RestaurantId: restaurantID})
	if status.Code(err) == codes.NotFound {
		return applications.Restaurant{}, applications.ErrKitchenNotFound
	}
	if err != nil {
		return applications.Restaurant{}, fmt.Errorf("get restaurant: %w", err)
	}
	restaurant := response.GetRestaurant()
	return applications.Restaurant{
		ID:      restaurant.GetRestaurantId(),
		Name:    restaurant.GetName(),
		Address: restaurant.GetAddress(),
		IsOpen:  restaurant.GetIsOpen(),
	}, nil
}

// MenuItemNames returns no names for a restaurant without a menu: the orders
// still show, with neutral item labels.
func (r *Restaurants) MenuItemNames(ctx context.Context, restaurantID string) (map[string]string, error) {
	response, err := r.client.GetMenu(identity.WithoutUser(ctx), &restaurantsv1.GetMenuRequest{RestaurantId: restaurantID})
	if status.Code(err) == codes.NotFound {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get menu: %w", err)
	}
	names := make(map[string]string, len(response.GetItems()))
	for _, item := range response.GetItems() {
		names[item.GetMenuItemId()] = item.GetName()
	}
	return names, nil
}

// boardError hides whether the restaurant exists from a user who may not
// operate its kitchen: both are ErrKitchenNotFound.
func boardError(operation string, err error) error {
	switch status.Code(err) {
	case codes.NotFound, codes.PermissionDenied:
		return applications.ErrKitchenNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// commandError names the refusals of restaurants; FAILED_PRECONDITION covers
// a forbidden transition and, on accept, a closed restaurant or an
// unavailable item: restaurants gives them one reason.
func commandError(operation string, err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return applications.ErrOrderNotFound
	case codes.PermissionDenied:
		return applications.ErrNotPermitted
	case codes.FailedPrecondition:
		return applications.ErrStageConflict
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", applications.ErrInvalidCommand, status.Convert(err).Message())
	}
	return fmt.Errorf("%s: %w", operation, err)
}
