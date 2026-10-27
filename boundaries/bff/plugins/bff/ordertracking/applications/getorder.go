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

// Deliveries reads the delivery of an Order — its courier and live
// tracking — from delivery on behalf of the customer in the request context
// (order#view); each part is nil without one.
type Deliveries interface {
	DeliveryOf(ctx context.Context, orderID string) (OrderDelivery, error)
}

// OrderDelivery is what the customer sees of the delivery of an Order.
type OrderDelivery struct {
	Courier  *Courier
	Tracking *Tracking
}

// Catalog reads public data of restaurants: no customer is involved.
type Catalog interface {
	Restaurant(ctx context.Context, restaurantID string) (Restaurant, error)
	// Restaurants reads the restaurants of a page in one call (ADR 7): the
	// found ones by id, an unknown id is absent.
	Restaurants(ctx context.Context, restaurantIDs []string) (map[string]Restaurant, error)
}

// GetOrder builds the customer view of one Order: orders on behalf of the
// customer with the item names kept at Place Order, restaurants for the
// restaurant.
type GetOrder struct {
	orders     OrderReader
	catalog    Catalog
	deliveries Deliveries
	now        func() time.Time
}

func NewGetOrder(orders OrderReader, catalog Catalog, deliveries Deliveries) GetOrder {
	return GetOrder{orders: orders, catalog: catalog, deliveries: deliveries, now: time.Now}
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
		delivery, err := h.deliveries.DeliveryOf(ctx, order.ID)
		if err != nil {
			return OrderView{}, fmt.Errorf("read delivery: %w", err)
		}
		view.Courier = delivery.Courier
		// A final Order has no courier on the way, whatever delivery still
		// reports.
		if !view.Status.Final() {
			view.Tracking = delivery.Tracking
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
		Priority:       order.Priority,
		PremiumFee:     order.PremiumFee,
		PlacedAt:       order.PlacedAt,
		Milestones:     milestones(order),
		AvailableUntil: availableUntil(order, current, now),
		Deadlines:      order.Deadlines,
	}, nil
}
