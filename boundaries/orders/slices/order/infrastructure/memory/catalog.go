package memory

import (
	"context"
	"fmt"
	"sync"

	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
)

// Catalog serves configured restaurant snapshots.
type Catalog struct {
	mu          sync.Mutex
	restaurants map[order.RestaurantID]placeorder.CatalogSnapshot
	failure     error
	calls       int
}

func NewCatalog() *Catalog {
	return &Catalog{restaurants: make(map[order.RestaurantID]placeorder.CatalogSnapshot)}
}

func (c *Catalog) Put(restaurantID order.RestaurantID, snapshot placeorder.CatalogSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.restaurants[restaurantID] = snapshot
}

func (c *Catalog) Fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failure = err
}

func (c *Catalog) Snapshot(_ context.Context, restaurantID order.RestaurantID, menuItemIDs []order.MenuItemID) (placeorder.CatalogSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.failure != nil {
		return placeorder.CatalogSnapshot{}, c.failure
	}
	snapshot, ok := c.restaurants[restaurantID]
	if !ok {
		return placeorder.CatalogSnapshot{}, fmt.Errorf("%w: %q", placeorder.ErrUnknownRestaurant, restaurantID)
	}
	requested := placeorder.CatalogSnapshot{
		Currency:     snapshot.Currency,
		Items:        make(map[order.MenuItemID]placeorder.CatalogItem, len(menuItemIDs)),
		MinimumTotal: snapshot.MinimumTotal,
		MaximumTotal: snapshot.MaximumTotal,
	}
	for _, id := range menuItemIDs {
		if item, known := snapshot.Items[id]; known {
			requested.Items[id] = item
		}
	}
	return requested, nil
}

func (c *Catalog) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}
