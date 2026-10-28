// Package infrastructure adapts the kitchen ports to restaurants.v1.RestaurantsService
// through the internal gateway: the kitchen board (ListActiveRestaurantOrders,
// GetKitchenQueue, GetCookPool), the kitchen and Cook Pool commands on behalf of the user — service
// token of BFF and user assertion (ADR 32) — and the public GetRestaurant and
// GetMenu without the user. restaurants authorizes restaurant#operate_kitchen.
package infrastructure

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	_ applications.CookCommands    = (*Restaurants)(nil)
)

// priorities has no UNSPECIFIED: restaurants always sets the priority.
var priorities = map[restaurantsv1.OrderPriority]applications.Priority{
	restaurantsv1.OrderPriority_ORDER_PRIORITY_STANDARD: applications.PriorityStandard,
	restaurantsv1.OrderPriority_ORDER_PRIORITY_PREMIUM:  applications.PriorityPremium,
}

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
		priority, ok := priorities[order.GetRestaurantOrder().GetPriority()]
		if !ok {
			return nil, fmt.Errorf("restaurant order %s has unexpected priority %s", order.GetRestaurantOrder().GetRestaurantOrderId(), order.GetRestaurantOrder().GetPriority())
		}
		lines := make([]applications.ActiveOrderLine, 0, len(order.GetItems()))
		for _, item := range order.GetItems() {
			lines = append(lines, applications.ActiveOrderLine{MenuItemID: item.GetMenuItemId(), Quantity: int(item.GetQuantity())})
		}
		active := applications.ActiveOrder{
			ID:              order.GetRestaurantOrder().GetRestaurantOrderId(),
			OrderID:         order.GetRestaurantOrder().GetOrderId(),
			Stage:           stage,
			Lines:           lines,
			AcceptedAt:      moment(order.GetAcceptedAt()),
			ExpectedReadyAt: moment(order.GetRestaurantOrder().GetExpectedReadyAt()),
			CookID:          order.GetRestaurantOrder().GetCookId(),
			Priority:        priority,
		}
		if schedule := order.GetRestaurantOrder().GetSchedule(); schedule != nil {
			window, err := windowOf(order.GetRestaurantOrder().GetRestaurantOrderId(), schedule)
			if err != nil {
				return nil, err
			}
			active.DeliveryWindow = &window
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

// ScheduledOrders keeps the user of ctx: restaurants checks
// restaurant#operate_kitchen, like the rest of the board.
func (r *Restaurants) ScheduledOrders(ctx context.Context, restaurantID string) ([]applications.ScheduledOrder, error) {
	response, err := r.client.ListScheduledOrders(ctx, &restaurantsv1.ListScheduledOrdersRequest{RestaurantId: restaurantID})
	if err != nil {
		return nil, boardError("list scheduled orders", err)
	}
	orders := make([]applications.ScheduledOrder, 0, len(response.GetOrders()))
	for _, order := range response.GetOrders() {
		priority, ok := priorities[order.GetPriority()]
		if !ok {
			return nil, fmt.Errorf("scheduled order %s has unexpected priority %s", order.GetRestaurantOrderId(), order.GetPriority())
		}
		schedule := order.GetSchedule()
		if schedule == nil || schedule.GetPlannedReadyAt() == nil {
			return nil, fmt.Errorf("scheduled order %s has no schedule", order.GetRestaurantOrderId())
		}
		window, err := windowOf(order.GetRestaurantOrderId(), schedule)
		if err != nil {
			return nil, err
		}
		lines := make([]applications.ActiveOrderLine, 0, len(order.GetItems()))
		for _, item := range order.GetItems() {
			lines = append(lines, applications.ActiveOrderLine{MenuItemID: item.GetMenuItemId(), Quantity: int(item.GetQuantity())})
		}
		orders = append(orders, applications.ScheduledOrder{
			ID:             order.GetRestaurantOrderId(),
			OrderID:        order.GetOrderId(),
			Lines:          lines,
			Priority:       priority,
			DeliveryWindow: window,
			PlannedReadyAt: schedule.GetPlannedReadyAt().AsTime(),
		})
	}
	return orders, nil
}

// windowOf requires both bounds of the window: a schedule without them breaks
// the contract of restaurants.
func windowOf(restaurantOrderID string, schedule *restaurantsv1.OrderSchedule) (applications.Window, error) {
	if schedule.GetWindowStart() == nil || schedule.GetWindowEnd() == nil {
		return applications.Window{}, fmt.Errorf("restaurant order %s has a window without bounds", restaurantOrderID)
	}
	return applications.Window{Start: schedule.GetWindowStart().AsTime(), End: schedule.GetWindowEnd().AsTime()}, nil
}

// CookPool keeps the user of ctx: restaurants checks restaurant#view_orders.
func (r *Restaurants) CookPool(ctx context.Context, restaurantID string) (applications.CookPool, error) {
	response, err := r.client.GetCookPool(ctx, &restaurantsv1.GetCookPoolRequest{RestaurantId: restaurantID})
	if err != nil {
		return applications.CookPool{}, boardError("get cook pool", err)
	}
	cooks := make([]applications.Cook, 0, len(response.GetCooks()))
	for _, cook := range response.GetCooks() {
		cooks = append(cooks, applications.Cook{
			ID:                    cook.GetCookId(),
			Name:                  cook.GetName(),
			OnLine:                cook.GetOnLine(),
			CurrentKitchenOrderID: cook.GetCurrentRestaurantOrderId(),
		})
	}
	return applications.CookPool{
		Size:   int(response.GetSize()),
		OnLine: int(response.GetOnLine()),
		Busy:   int(response.GetBusy()),
		Queued: int(response.GetQueued()),
		Cooks:  cooks,
	}, nil
}

// Hire keeps the user of ctx: restaurants checks restaurant#manage_cooks.
func (r *Restaurants) Hire(ctx context.Context, restaurantID, name string) error {
	_, err := r.client.HireCook(ctx, &restaurantsv1.HireCookRequest{RestaurantId: restaurantID, Name: name})
	return cookError("hire cook", err, codes.InvalidArgument, applications.ErrInvalidCookName)
}

// SetOnLine: FAILED_PRECONDITION is a busy cook off line and a retired cook
// on line.
func (r *Restaurants) SetOnLine(ctx context.Context, restaurantID, cookID string, onLine bool) error {
	if onLine {
		_, err := r.client.PutCookOnLine(ctx, &restaurantsv1.PutCookOnLineRequest{RestaurantId: restaurantID, CookId: cookID})
		return cookError("put cook on line", err, codes.FailedPrecondition, applications.ErrCookRetired)
	}
	_, err := r.client.TakeCookOffLine(ctx, &restaurantsv1.TakeCookOffLineRequest{RestaurantId: restaurantID, CookId: cookID})
	return cookError("take cook off line", err, codes.FailedPrecondition, applications.ErrCookBusy)
}

func (r *Restaurants) Retire(ctx context.Context, restaurantID, cookID string) error {
	_, err := r.client.RetireCook(ctx, &restaurantsv1.RetireCookRequest{RestaurantId: restaurantID, CookId: cookID})
	return cookError("retire cook", err, codes.FailedPrecondition, applications.ErrCookOnLine)
}

// cookError names the refusals of the Cook Pool commands: refusal is the
// status of the one business refusal of the command, refused what it means.
func cookError(operation string, err error, refusal codes.Code, refused error) error {
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.NotFound:
		return applications.ErrCookNotFound
	case codes.PermissionDenied:
		return applications.ErrNotPermitted
	case refusal:
		return refused
	}
	return fmt.Errorf("%s: %w", operation, err)
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

// StartNext keeps the user of ctx like Apply; an unset restaurant_order is an
// empty Kitchen Queue.
func (r *Restaurants) StartNext(ctx context.Context, restaurantID, requestID string) (string, error) {
	response, err := r.client.StartNextPreparation(ctx, &restaurantsv1.StartNextPreparationRequest{RestaurantId: restaurantID, RequestId: requestID})
	if err != nil {
		return "", commandError("start next preparation", err)
	}
	return response.GetRestaurantOrder().GetRestaurantOrderId(), nil
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
		ID:     restaurant.GetRestaurantId(),
		Name:   restaurant.GetName(),
		IsOpen: restaurant.GetIsOpen(),
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
// unavailable item: restaurants gives them one reason. RESOURCE_EXHAUSTED is
// a start of preparation without a free cook (FR-225).
func commandError(operation string, err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return applications.ErrOrderNotFound
	case codes.PermissionDenied:
		return applications.ErrNotPermitted
	case codes.FailedPrecondition:
		return applications.ErrStageConflict
	case codes.ResourceExhausted:
		return applications.ErrNoCookAvailable
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", applications.ErrInvalidCommand, status.Convert(err).Message())
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// moment reads an optional moment: an unset Timestamp is one that has not
// happened yet (before Acceptance).
func moment(value *timestamppb.Timestamp) *time.Time {
	if value == nil {
		return nil
	}
	t := value.AsTime()
	return &t
}
