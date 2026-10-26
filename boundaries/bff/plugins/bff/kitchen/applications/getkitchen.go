package applications

import (
	"context"
	"fmt"
	"slices"
)

// GetKitchen builds the kitchen screen: the board and the Cook Pool of
// restaurants on behalf of the user, the restaurant and the names of menu
// items from the public reads.
type GetKitchen struct {
	board   KitchenBoard
	catalog Catalog
}

func NewGetKitchen(board KitchenBoard, catalog Catalog) GetKitchen {
	return GetKitchen{board: board, catalog: catalog}
}

// Handle returns ErrKitchenNotFound for a missing restaurant or a user who
// may not operate its kitchen. The board is read first, so restaurants
// authorizes the user before any public read.
func (h GetKitchen) Handle(ctx context.Context, restaurantID string) (KitchenView, error) {
	active, err := h.board.ActiveOrders(ctx, restaurantID)
	if err != nil {
		return KitchenView{}, err
	}
	queue, err := h.board.QueueOrder(ctx, restaurantID)
	if err != nil {
		return KitchenView{}, err
	}
	restaurant, err := h.catalog.Restaurant(ctx, restaurantID)
	if err != nil {
		return KitchenView{}, err
	}
	names, err := h.catalog.MenuItemNames(ctx, restaurantID)
	if err != nil {
		return KitchenView{}, fmt.Errorf("read menu: %w", err)
	}
	pool, err := h.board.CookPool(ctx, restaurantID)
	if err != nil {
		return KitchenView{}, fmt.Errorf("read cook pool: %w", err)
	}
	cooks := cooksByID(pool)
	ordered := boardOrder(active, queue)
	orders := make([]OrderView, 0, len(ordered))
	for _, order := range ordered {
		orders = append(orders, orderView(order, names, cooks))
	}
	return KitchenView{Restaurant: restaurant, Orders: orders, CookPool: pool}, nil
}

// boardOrder puts RECEIVED orders first by ReceivedAt, then the QUEUED ones in
// the Kitchen Queue order, then the rest by ReceivedAt. active is already by
// ReceivedAt; a QUEUED order the queue read missed (it changed between the two
// reads) follows the queue by ReceivedAt.
func boardOrder(active []ActiveOrder, queue []string) []ActiveOrder {
	position := make(map[string]int, len(queue))
	for i, id := range queue {
		position[id] = i
	}
	group := func(order ActiveOrder) int {
		switch order.Stage {
		case StageReceived:
			return 0
		case StageQueued:
			return 1
		default:
			return 2
		}
	}
	queuePosition := func(order ActiveOrder) int {
		if p, ok := position[order.ID]; ok {
			return p
		}
		return len(queue)
	}
	result := slices.Clone(active)
	slices.SortStableFunc(result, func(a, b ActiveOrder) int {
		if d := group(a) - group(b); d != 0 {
			return d
		}
		if group(a) == 1 {
			return queuePosition(a) - queuePosition(b)
		}
		return 0
	})
	return result
}
