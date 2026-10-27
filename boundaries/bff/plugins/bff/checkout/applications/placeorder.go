// Package applications holds the checkout scenario of the customer UI: Place
// Order on behalf of the customer, then the customer view of the placed Order.
// orders owns validation, prices and the start of fulfillment; BFF returns the
// view or the business problems orders named. Protobuf and GraphQL types do not
// reach it (BFF ADR 2).
package applications

import (
	"context"
	"errors"
	"fmt"

	tracking "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

var (
	// ErrRestaurantNotFound is a restaurant orders could not find in restaurants.
	ErrRestaurantNotFound = errors.New("restaurant not found")
	// ErrInvalidOrder is input orders rejected as malformed: no problem the
	// customer can fix in the cart, but a defect of the client.
	ErrInvalidOrder = errors.New("invalid order")
)

// ProblemCode is a business reason why orders did not place the Order
// (PlaceOrderProblemCode of the public schema).
type ProblemCode string

const (
	ProblemOrderItemUnavailable         ProblemCode = "ORDER_ITEM_UNAVAILABLE"
	ProblemOrderTotalBelowMinimum       ProblemCode = "ORDER_TOTAL_BELOW_MINIMUM"
	ProblemOrderTotalAboveMaximum       ProblemCode = "ORDER_TOTAL_ABOVE_MAXIMUM"
	ProblemRestaurantNotAcceptingOrders ProblemCode = "RESTAURANT_NOT_ACCEPTING_ORDERS"
	ProblemPremiumUnavailable           ProblemCode = "PREMIUM_UNAVAILABLE"
)

// Problem refers to Menu Items, or to the whole Order when MenuItemIDs is empty.
type Problem struct {
	Code        ProblemCode
	MenuItemIDs []string
}

// PlacementRefused is a business refusal of orders. Reason is its ErrorInfo
// reason; Codes and MenuItemIDs come from the metadata of ORDER_NOT_ORDERABLE.
type PlacementRefused struct {
	Reason      string
	Codes       []string
	MenuItemIDs []string
}

func (e PlacementRefused) Error() string { return "placement refused: " + e.Reason }

type Line struct {
	MenuItemID string
	Quantity   int
}

// Priority is the Premium Priority chosen at checkout (orders FR-650).
type Priority string

const (
	PriorityStandard Priority = "standard"
	PriorityPremium  Priority = "premium"
)

type Input struct {
	RestaurantID string
	Lines        []Line
	Priority     Priority
	// IdempotencyKey identifies the checkout attempt; a retry with the same key
	// returns the Order placed by the first attempt.
	IdempotencyKey string
}

// OrderPlacer is the Place Order command of orders on behalf of the customer
// in the request context.
type OrderPlacer interface {
	// PlaceOrder returns PlacementRefused for a business refusal.
	PlaceOrder(ctx context.Context, input Input) (orderID string, err error)
}

// OrderViews builds the customer view of an Order: the GetOrder scenario of
// order tracking.
type OrderViews interface {
	Handle(ctx context.Context, orderID string) (tracking.OrderView, error)
}

// Result carries either the placed Order or the problems.
type Result struct {
	Order    *tracking.OrderView
	Problems []Problem
}

type PlaceOrder struct {
	orders OrderPlacer
	views  OrderViews
}

func NewPlaceOrder(orders OrderPlacer, views OrderViews) PlaceOrder {
	return PlaceOrder{orders: orders, views: views}
}

// Handle places the Order and reads it back for the payload. A refusal the
// public schema cannot name stays an error.
func (h PlaceOrder) Handle(ctx context.Context, input Input) (Result, error) {
	orderID, err := h.orders.PlaceOrder(ctx, input)
	var refused PlacementRefused
	if errors.As(err, &refused) {
		problems := problemsOf(refused)
		if len(problems) > 0 {
			return Result{Problems: problems}, nil
		}
	}
	if err != nil {
		return Result{}, err
	}
	view, err := h.views.Handle(ctx, orderID)
	if err != nil {
		// orders commits the Order before it answers PlaceOrder.
		return Result{}, fmt.Errorf("read placed order %s: %w", orderID, err)
	}
	return Result{Order: &view}, nil
}

var orderableProblems = map[string]ProblemCode{
	string(ProblemOrderItemUnavailable):   ProblemOrderItemUnavailable,
	string(ProblemOrderTotalBelowMinimum): ProblemOrderTotalBelowMinimum,
	string(ProblemOrderTotalAboveMaximum): ProblemOrderTotalAboveMaximum,
}

// problemsOf gives one problem per known code. Only unavailable items refer to
// Menu Items, so the item ids of ORDER_NOT_ORDERABLE belong to that problem;
// the total limits refer to the whole Order.
func problemsOf(refused PlacementRefused) []Problem {
	switch refused.Reason {
	case "RESTAURANT_NOT_ACCEPTING_ORDERS":
		return []Problem{{Code: ProblemRestaurantNotAcceptingOrders, MenuItemIDs: []string{}}}
	case "PREMIUM_UNAVAILABLE":
		return []Problem{{Code: ProblemPremiumUnavailable, MenuItemIDs: []string{}}}
	case "ORDER_NOT_ORDERABLE":
		problems := make([]Problem, 0, len(refused.Codes))
		for _, reason := range refused.Codes {
			code, known := orderableProblems[reason]
			if !known {
				continue
			}
			problem := Problem{Code: code, MenuItemIDs: []string{}}
			if code == ProblemOrderItemUnavailable {
				problem.MenuItemIDs = append(problem.MenuItemIDs, refused.MenuItemIDs...)
			}
			problems = append(problems, problem)
		}
		return problems
	}
	return nil
}
