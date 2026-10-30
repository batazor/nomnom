package applications

import (
	"context"
	"errors"
	"fmt"
)

// Problem is a business reason why a kitchen command cannot be applied.
type Problem string

const (
	ProblemOrderStageConflict Problem = "order_stage_conflict"
	ProblemNoCookAvailable    Problem = "no_cook_available"
)

// ActionRequest is one kitchen command of the screen.
type ActionRequest struct {
	RestaurantID   string
	KitchenOrderID string
	Action         Action
}

// ActionResult carries either the order after the command or the problem.
// Order is nil with a problem and when the order left the kitchen (REJECT).
type ActionResult struct {
	Order   *OrderView
	Problem Problem
}

var knownActions = []Action{ActionAccept, ActionReject, ActionMarkAssembled, ActionMarkReady}

// ApplyKitchenAction runs a kitchen command and reads the order back from the
// kitchen board: the command answers no items and no receipt time.
type ApplyKitchenAction struct {
	commands KitchenCommands
	board    KitchenBoard
	catalog  Catalog
}

func NewApplyKitchenAction(commands KitchenCommands, board KitchenBoard, catalog Catalog) ApplyKitchenAction {
	return ApplyKitchenAction{commands: commands, board: board, catalog: catalog}
}

// Handle answers a stage conflict and a start without a free cook as
// problems; the other refusals of
// restaurants stay errors (ErrOrderNotFound, ErrNotPermitted,
// ErrInvalidCommand).
func (h ApplyKitchenAction) Handle(ctx context.Context, request ActionRequest) (ActionResult, error) {
	known := false
	for _, action := range knownActions {
		known = known || action == request.Action
	}
	if !known {
		return ActionResult{}, fmt.Errorf("%w %q", ErrUnknownAction, request.Action)
	}
	err := h.commands.Apply(ctx, request.RestaurantID, request.KitchenOrderID, request.Action)
	if errors.Is(err, ErrStageConflict) {
		return ActionResult{Problem: ProblemOrderStageConflict}, nil
	}
	if errors.Is(err, ErrNoCookAvailable) {
		return ActionResult{Problem: ProblemNoCookAvailable}, nil
	}
	if err != nil {
		return ActionResult{}, err
	}
	if request.Action == ActionReject {
		return ActionResult{}, nil
	}
	return readBack(ctx, h.board, h.catalog, request.RestaurantID, request.KitchenOrderID)
}

// readBack reads the order after a command from the kitchen board: the
// command answers no items and no receipt time.
func readBack(ctx context.Context, board KitchenBoard, catalog Catalog, restaurantID, kitchenOrderID string) (ActionResult, error) {
	active, err := board.ActiveOrders(ctx, restaurantID)
	if err != nil {
		return ActionResult{}, fmt.Errorf("read the order back: %w", err)
	}
	for _, order := range active {
		if order.ID != kitchenOrderID {
			continue
		}
		names, err := catalog.MenuItemNames(ctx, restaurantID)
		if err != nil {
			return ActionResult{}, fmt.Errorf("read menu: %w", err)
		}
		pool, err := board.CookPool(ctx, restaurantID)
		if err != nil {
			return ActionResult{}, fmt.Errorf("read cook pool: %w", err)
		}
		view := orderView(order, names, cooksByID(pool))
		return ActionResult{Order: &view}, nil
	}
	// The order left the kitchen between the command and the read.
	return ActionResult{}, nil
}

// StartNextRequest takes the head of the Kitchen Queue of a restaurant.
type StartNextRequest struct {
	RestaurantID string
	// RequestID is chosen per attempt and reused on a manual retry.
	RequestID string
}

// StartNextPreparation starts the head of the Kitchen Queue (restaurants
// FR-225): the kitchen does not choose the order. An empty queue is an empty
// result, not a problem; no free cook is ProblemNoCookAvailable.
type StartNextPreparation struct {
	commands KitchenCommands
	board    KitchenBoard
	catalog  Catalog
}

func NewStartNextPreparation(commands KitchenCommands, board KitchenBoard, catalog Catalog) StartNextPreparation {
	return StartNextPreparation{commands: commands, board: board, catalog: catalog}
}

func (h StartNextPreparation) Handle(ctx context.Context, request StartNextRequest) (ActionResult, error) {
	started, err := h.commands.StartNext(ctx, request.RestaurantID, request.RequestID)
	if errors.Is(err, ErrNoCookAvailable) {
		return ActionResult{Problem: ProblemNoCookAvailable}, nil
	}
	if err != nil {
		return ActionResult{}, err
	}
	if started == "" {
		return ActionResult{}, nil
	}
	return readBack(ctx, h.board, h.catalog, request.RestaurantID, started)
}
