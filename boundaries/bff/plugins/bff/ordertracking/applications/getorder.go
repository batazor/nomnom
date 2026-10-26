package applications

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// OrderReader reads one Order on behalf of the customer in the request
// context; a missing or foreign Order is ErrOrderNotFound.
type OrderReader interface {
	GetOrder(ctx context.Context, orderID string) (OrderSnapshot, error)
}

// Couriers reads the courier of an Order from delivery on behalf of the
// customer in the request context (order#view); nil without one.
type Couriers interface {
	CourierOf(ctx context.Context, orderID string) (*Courier, error)
}

// Catalog reads public data of restaurants: no customer is involved.
type Catalog interface {
	Restaurant(ctx context.Context, restaurantID string) (Restaurant, error)
}

// GetOrder builds the customer view of one Order: orders on behalf of the
// customer with the item names kept at Place Order, restaurants for the
// restaurant.
type GetOrder struct {
	orders   OrderReader
	catalog  Catalog
	couriers Couriers
	now      func() time.Time
}

func NewGetOrder(orders OrderReader, catalog Catalog, couriers Couriers) GetOrder {
	return GetOrder{orders: orders, catalog: catalog, couriers: couriers, now: time.Now}
}

// Handle returns ErrOrderNotFound for a missing or foreign Order.
func (h GetOrder) Handle(ctx context.Context, orderID string) (OrderView, error) {
	order, err := h.orders.GetOrder(ctx, orderID)
	if err != nil {
		return OrderView{}, err
	}
	restaurant, err := h.catalog.Restaurant(ctx, order.RestaurantID)
	if err != nil {
		return OrderView{}, fmt.Errorf("read restaurant: %w", err)
	}
	view, err := compose(order, restaurant, h.now())
	if err != nil {
		return OrderView{}, err
	}
	// Only an Order with an Assignment has a courier to ask delivery about.
	if slices.ContainsFunc(view.Milestones, func(milestone Milestone) bool {
		return milestone.Kind == MilestoneCourierAssigned
	}) {
		view.Courier, err = h.couriers.CourierOf(ctx, order.ID)
		if err != nil {
			return OrderView{}, fmt.Errorf("read courier: %w", err)
		}
	}
	return view, nil
}

// compose builds the customer view of an Order and its restaurant at now.
func compose(order OrderSnapshot, restaurant Restaurant, now time.Time) (OrderView, error) {
	current, err := status(order)
	if err != nil {
		return OrderView{}, err
	}
	items := make([]ItemView, 0, len(order.Items))
	for _, line := range order.Items {
		total, err := lineTotal(line.UnitPrice, line.Quantity)
		if err != nil {
			return OrderView{}, err
		}
		items = append(items, ItemView{
			MenuItemID: line.MenuItemID,
			Name:       line.Name,
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
		AvailableUntil: availableUntil(order, current, now),
		Deadlines:      order.Deadlines,
	}, nil
}
