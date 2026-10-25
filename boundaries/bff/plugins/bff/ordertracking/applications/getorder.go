package applications

import (
	"context"
	"fmt"
	"time"
)

// OrderReader reads one Order on behalf of the customer in the request
// context; a missing or foreign Order is ErrOrderNotFound.
type OrderReader interface {
	GetOrder(ctx context.Context, orderID string) (OrderSnapshot, error)
}

// Catalog reads public data of restaurants: no customer is involved.
type Catalog interface {
	Restaurant(ctx context.Context, restaurantID string) (Restaurant, error)
	// MenuItemNames returns the current names of menu items by id.
	MenuItemNames(ctx context.Context, restaurantID string) (map[string]string, error)
}

// GetOrder builds the customer view of one Order: orders on behalf of the
// customer, restaurants for the restaurant and the names of menu items.
type GetOrder struct {
	orders  OrderReader
	catalog Catalog
	now     func() time.Time
}

func NewGetOrder(orders OrderReader, catalog Catalog) GetOrder {
	return GetOrder{orders: orders, catalog: catalog, now: time.Now}
}

// Handle returns ErrOrderNotFound for a missing or foreign Order.
func (h GetOrder) Handle(ctx context.Context, orderID string) (OrderView, error) {
	order, err := h.orders.GetOrder(ctx, orderID)
	if err != nil {
		return OrderView{}, err
	}
	current, err := status(order)
	if err != nil {
		return OrderView{}, err
	}
	restaurant, err := h.catalog.Restaurant(ctx, order.RestaurantID)
	if err != nil {
		return OrderView{}, fmt.Errorf("read restaurant: %w", err)
	}
	names, err := h.catalog.MenuItemNames(ctx, order.RestaurantID)
	if err != nil {
		return OrderView{}, fmt.Errorf("read menu: %w", err)
	}
	items := make([]ItemView, 0, len(order.Items))
	for _, line := range order.Items {
		total, err := lineTotal(line.UnitPrice, line.Quantity)
		if err != nil {
			return OrderView{}, err
		}
		items = append(items, ItemView{
			MenuItemID: line.MenuItemID,
			Name:       names[line.MenuItemID],
			Quantity:   line.Quantity,
			UnitPrice:  line.UnitPrice,
			LineTotal:  total,
		})
	}
	return OrderView{
		ID:             order.ID,
		Status:         current,
		EndReason:      endReason(order),
		Restaurant:     restaurant,
		Items:          items,
		Total:          order.Total,
		PlacedAt:       order.PlacedAt,
		Milestones:     milestones(order),
		AvailableUntil: availableUntil(order, current, h.now()),
		Deadlines:      order.Deadlines,
	}, nil
}
