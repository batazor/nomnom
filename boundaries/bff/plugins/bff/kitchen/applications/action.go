package applications

import (
	"context"
	"errors"
	"fmt"
)

// Problem is a business reason why a kitchen command cannot be applied.
type Problem string

const ProblemOrderStageConflict Problem = "order_stage_conflict"

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

var knownActions = []Action{ActionAccept, ActionReject, ActionStartPreparation, ActionMarkAssembled, ActionMarkReady}

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

// Handle answers a stage conflict as a problem; the other refusals of
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
	if err != nil {
		return ActionResult{}, err
	}
	if request.Action == ActionReject {
		return ActionResult{}, nil
	}
	active, err := h.board.ActiveOrders(ctx, request.RestaurantID)
	if err != nil {
		return ActionResult{}, fmt.Errorf("read the order back: %w", err)
	}
	for _, order := range active {
		if order.ID != request.KitchenOrderID {
			continue
		}
		names, err := h.catalog.MenuItemNames(ctx, request.RestaurantID)
		if err != nil {
			return ActionResult{}, fmt.Errorf("read menu: %w", err)
		}
		view := orderView(order, names)
		return ActionResult{Order: &view}, nil
	}
	// The order left the kitchen between the command and the read.
	return ActionResult{}, nil
}
