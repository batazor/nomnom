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
	ActionAccept        Action = "accept"
	ActionReject        Action = "reject"
	ActionMarkAssembled Action = "mark_assembled"
	ActionMarkReady     Action = "mark_ready"
)

// Priority is the Premium Priority of a restaurant order: premium orders are
// taken first from the Kitchen Queue.
type Priority string

const (
	PriorityStandard Priority = "standard"
	PriorityPremium  Priority = "premium"
)

// Restaurant is the public data of the restaurant the kitchen belongs to.
type Restaurant struct {
	ID     string
	Name   string
	IsOpen bool
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
	// AcceptedAt is nil before Acceptance (StageReceived).
	AcceptedAt *time.Time
	// ExpectedReadyAt is nil before Acceptance; it includes the reported
	// preparation delays.
	ExpectedReadyAt *time.Time
	// CookID is the cook assigned at the start of preparation; empty before.
	CookID   string
	Priority Priority
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
	CookPoolReader
}

// KitchenCommands runs the kitchen commands of restaurants on behalf of the
// user in the context. Apply returns ErrOrderNotFound, ErrNotPermitted,
// ErrInvalidCommand or ErrStageConflict for the refusals of restaurants, and
// ErrNoCookAvailable when no cook on line is free to start preparing.
type KitchenCommands interface {
	Apply(ctx context.Context, restaurantID, kitchenOrderID string, action Action) error
	// StartNext starts the head of the Kitchen Queue and returns its id;
	// empty when the queue is empty. A repeat with requestID starts no other
	// order. ErrNoCookAvailable without a free cook on line.
	StartNext(ctx context.Context, restaurantID, requestID string) (string, error)
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
	// AcceptedAt and ExpectedReadyAt bound the cooking progress; both are nil
	// before Acceptance.
	AcceptedAt      *time.Time
	ExpectedReadyAt *time.Time
	// Cook is nil before the start of preparation and for a cook who left
	// the pool.
	Cook     *Cook
	Priority Priority
}

// KitchenView is the kitchen screen of one restaurant.
type KitchenView struct {
	Restaurant Restaurant
	Orders     []OrderView
	CookPool   CookPool
}

func orderView(order ActiveOrder, names map[string]string, cooks map[string]Cook) OrderView {
	items := make([]ItemView, 0, len(order.Lines))
	for _, line := range order.Lines {
		items = append(items, ItemView{Name: names[line.MenuItemID], Quantity: line.Quantity})
	}
	view := OrderView{
		ID:              order.ID,
		OrderID:         order.OrderID,
		Stage:           order.Stage,
		Items:           items,
		ReceivedAt:      order.ReceivedAt,
		AcceptedAt:      order.AcceptedAt,
		ExpectedReadyAt: order.ExpectedReadyAt,
		Priority:        order.Priority,
	}
	if cook, ok := cooks[order.CookID]; ok {
		view.Cook = &cook
	}
	return view
}
