// Package applications holds the scenarios of the kitchen screen: the active
// orders of one restaurant and the kitchen commands over them. restaurants
// authorizes the user (restaurant#operate_kitchen); BFF only forwards the
// verified user. Protobuf and GraphQL types do not reach it (BFF ADR 2).
package applications

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrKitchenNotFound is a restaurant that does not exist or whose kitchen
	// the user may not operate: the screen shows neither.
	ErrKitchenNotFound = errors.New("kitchen not found")
	// ErrOrderNotFound is a restaurant order unknown to the restaurant.
	ErrOrderNotFound = errors.New("restaurant order not found")
	// ErrNotPermitted is a user who may not operate the kitchen.
	ErrNotPermitted = errors.New("kitchen operation is not permitted")
	// ErrInvalidCommand is a command restaurants rejects as malformed.
	ErrInvalidCommand = errors.New("kitchen command is invalid")
	// ErrStageConflict is a command the current stage of the order forbids.
	ErrStageConflict = errors.New("restaurant order stage forbids the command")
	// ErrUnknownAction is an action outside the kitchen commands.
	ErrUnknownAction = errors.New("unknown kitchen action")
)

// Stage is the stage of a restaurant order in the kitchen.
type Stage string

const (
	StageReceived  Stage = "received"
	StageQueued    Stage = "queued"
	StagePreparing Stage = "preparing"
	StageAssembled Stage = "assembled"
	StageReady     Stage = "ready"
)

// Action is a kitchen command over one restaurant order.
type Action string

const (
	ActionAccept           Action = "accept"
	ActionReject           Action = "reject"
	ActionStartPreparation Action = "start_preparation"
	ActionMarkAssembled    Action = "mark_assembled"
	ActionMarkReady        Action = "mark_ready"
)

// Restaurant is the public data of the restaurant the kitchen belongs to.
type Restaurant struct {
	ID      string
	Name    string
	Address string
	IsOpen  bool
}

// ActiveOrderLine is a line of an active order as restaurants keeps it.
type ActiveOrderLine struct {
	MenuItemID string
	Quantity   int
}

// ActiveOrder is a restaurant order the kitchen still works on.
type ActiveOrder struct {
	ID         string
	OrderID    string
	Stage      Stage
	Lines      []ActiveOrderLine
	ReceivedAt time.Time
}

// KitchenBoard reads the kitchen board of restaurants on behalf of the user
// in the context. Both return ErrKitchenNotFound for a missing restaurant or
// a user who may not operate its kitchen.
type KitchenBoard interface {
	// ActiveOrders lists the orders from RECEIVED to READY by ascending
	// ReceivedAt.
	ActiveOrders(ctx context.Context, restaurantID string) ([]ActiveOrder, error)
	// QueueOrder returns the ids of the QUEUED orders in the recommended
	// order of the Kitchen Queue.
	QueueOrder(ctx context.Context, restaurantID string) ([]string, error)
}

// KitchenCommands runs the kitchen commands of restaurants on behalf of the
// user in the context. Apply returns ErrOrderNotFound, ErrNotPermitted,
// ErrInvalidCommand or ErrStageConflict for the refusals of restaurants.
type KitchenCommands interface {
	Apply(ctx context.Context, restaurantID, kitchenOrderID string, action Action) error
}

// Catalog reads public data of restaurants: no user is involved.
type Catalog interface {
	// Restaurant returns ErrKitchenNotFound for an unknown restaurant.
	Restaurant(ctx context.Context, restaurantID string) (Restaurant, error)
	// MenuItemNames returns the current names of menu items by id.
	MenuItemNames(ctx context.Context, restaurantID string) (map[string]string, error)
}

// ItemView is a line of a kitchen order with the current menu item name,
// empty when the item is no longer in the menu.
type ItemView struct {
	Name     string
	Quantity int
}

// OrderView is a restaurant order as the kitchen screen shows it.
type OrderView struct {
	ID         string
	OrderID    string
	Stage      Stage
	Items      []ItemView
	ReceivedAt time.Time
}

// KitchenView is the kitchen screen of one restaurant.
type KitchenView struct {
	Restaurant Restaurant
	Orders     []OrderView
}

func orderView(order ActiveOrder, names map[string]string) OrderView {
	items := make([]ItemView, 0, len(order.Lines))
	for _, line := range order.Lines {
		items = append(items, ItemView{Name: names[line.MenuItemID], Quantity: line.Quantity})
	}
	return OrderView{
		ID:         order.ID,
		OrderID:    order.OrderID,
		Stage:      order.Stage,
		Items:      items,
		ReceivedAt: order.ReceivedAt,
	}
}
