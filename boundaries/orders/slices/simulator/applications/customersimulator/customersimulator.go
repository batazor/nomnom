// Package customersimulator is the demo customer of the Simulator (FR-605):
// while the Simulator is enabled it places an Order after a random delay in an
// open restaurant through the real Place Order use case and, in 10% of the
// cases, requests its cancellation after another delay while it is still
// placed. Failed use cases are logged; the next action is scheduled anyway.
package customersimulator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/applications/requestordercancellation"
	"nomnom/orders/slices/order/domains/order"
)

const (
	// cancellationProbability is the share of placed Orders the customer cancels.
	cancellationProbability = 0.10
	maxItems                = 3
	maxQuantity             = 2
	cancellationReason      = "simulated customer changed their mind"
)

// Switch reports whether the Simulator is enabled.
type Switch interface {
	Enabled(ctx context.Context) (bool, error)
}

// Catalog lists what the customer can order.
type Catalog interface {
	OpenRestaurants(ctx context.Context) ([]order.RestaurantID, error)
	AvailableMenuItems(ctx context.Context, restaurantID order.RestaurantID) ([]order.MenuItemID, error)
}

// Random is the source of every simulated choice; *math/rand/v2.Rand satisfies it.
type Random interface {
	IntN(n int) int
	Int64N(n int64) int64
	Float64() float64
}

// Keys generates idempotency keys of placements and Cancellation Requests.
type Keys interface {
	NewKey() string
}

type Dependencies struct {
	Switch              Switch
	Catalog             Catalog
	PlaceOrder          application.Handler[placeorder.Input, placeorder.Result]
	GetOrder            application.Handler[getorder.Query, getorder.OrderView]
	RequestCancellation application.Handler[requestordercancellation.Input, requestordercancellation.Result]
	Random              Random
	Keys                Keys
	CustomerID          string
	MinDelay            time.Duration
	MaxDelay            time.Duration
}

type pendingCancellation struct {
	orderID order.ID
	dueAt   time.Time
}

// Customer keeps the schedule of the simulated customer between ticks. Tick
// is called from one goroutine only.
type Customer struct {
	deps          Dependencies
	nextOrderAt   time.Time
	cancellations []pendingCancellation
}

func New(deps Dependencies) *Customer {
	return &Customer{deps: deps}
}

func (c *Customer) subject() application.Subject {
	return application.Subject{Kind: application.SubjectUser, ID: c.deps.CustomerID}
}

// Tick performs the actions due at now. A disabled Simulator drops the
// schedule, so enabling it again starts with a fresh delay.
func (c *Customer) Tick(ctx context.Context, now time.Time) {
	enabled, err := c.deps.Switch.Enabled(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "simulator: read state failed", telemetry.LogErrorKey, err)
		return
	}
	if !enabled {
		c.nextOrderAt = time.Time{}
		c.cancellations = nil
		return
	}
	if c.nextOrderAt.IsZero() {
		c.nextOrderAt = now.Add(c.delay())
	} else if !now.Before(c.nextOrderAt) {
		c.nextOrderAt = now.Add(c.delay())
		c.placeOrder(ctx, now)
	}
	c.cancelDue(ctx, now)
}

// delay is uniform in [MinDelay, MaxDelay].
func (c *Customer) delay() time.Duration {
	return c.deps.MinDelay + time.Duration(c.deps.Random.Int64N(int64(c.deps.MaxDelay-c.deps.MinDelay)+1))
}

func (c *Customer) placeOrder(ctx context.Context, now time.Time) {
	input, err := c.chooseOrder(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "simulator: choose order failed", telemetry.LogErrorKey, err)
		return
	}
	result, err := c.deps.PlaceOrder.Handle(ctx, input)
	if err != nil {
		slog.ErrorContext(ctx, "simulator: place order failed",
			slog.String("restaurant_id", string(input.RestaurantID)), telemetry.LogErrorKey, err)
		return
	}
	slog.InfoContext(ctx, "simulator: order placed", slog.String("order_id", string(result.OrderID)))
	if c.deps.Random.Float64() < cancellationProbability {
		c.cancellations = append(c.cancellations, pendingCancellation{orderID: result.OrderID, dueAt: now.Add(c.delay())})
	}
}

func (c *Customer) chooseOrder(ctx context.Context) (placeorder.Input, error) {
	restaurants, err := c.deps.Catalog.OpenRestaurants(ctx)
	if err != nil {
		return placeorder.Input{}, err
	}
	if len(restaurants) == 0 {
		return placeorder.Input{}, fmt.Errorf("no open restaurant")
	}
	restaurantID := restaurants[c.deps.Random.IntN(len(restaurants))]
	menu, err := c.deps.Catalog.AvailableMenuItems(ctx, restaurantID)
	if err != nil {
		return placeorder.Input{}, err
	}
	if len(menu) == 0 {
		return placeorder.Input{}, fmt.Errorf("restaurant %q has no available menu item", restaurantID)
	}
	// Partial Fisher–Yates: the first count items are distinct random picks.
	candidates := append([]order.MenuItemID(nil), menu...)
	count := 1 + c.deps.Random.IntN(min(maxItems, len(candidates)))
	items := make([]placeorder.ItemInput, 0, count)
	for i := range count {
		j := i + c.deps.Random.IntN(len(candidates)-i)
		candidates[i], candidates[j] = candidates[j], candidates[i]
		items = append(items, placeorder.ItemInput{MenuItemID: candidates[i], Quantity: 1 + c.deps.Random.IntN(maxQuantity)})
	}
	return placeorder.Input{
		Subject:        c.subject(),
		IdempotencyKey: c.deps.Keys.NewKey(),
		RestaurantID:   restaurantID,
		Items:          items,
	}, nil
}

// cancelDue requests the due cancellations of Orders that are still placed;
// each is attempted once.
func (c *Customer) cancelDue(ctx context.Context, now time.Time) {
	remaining := c.cancellations[:0]
	for _, pending := range c.cancellations {
		if now.Before(pending.dueAt) {
			remaining = append(remaining, pending)
			continue
		}
		c.cancel(ctx, pending.orderID)
	}
	c.cancellations = remaining
}

func (c *Customer) cancel(ctx context.Context, orderID order.ID) {
	view, err := c.deps.GetOrder.Handle(ctx, getorder.Query{Subject: c.subject(), OrderID: orderID})
	if err != nil {
		slog.ErrorContext(ctx, "simulator: get order failed", slog.String("order_id", string(orderID)), telemetry.LogErrorKey, err)
		return
	}
	if view.State != order.StatePlaced {
		return
	}
	result, err := c.deps.RequestCancellation.Handle(ctx, requestordercancellation.Input{
		Subject:   c.subject(),
		OrderID:   orderID,
		RequestID: c.deps.Keys.NewKey(),
		Reason:    cancellationReason,
	})
	if err != nil {
		slog.ErrorContext(ctx, "simulator: request cancellation failed", slog.String("order_id", string(orderID)), telemetry.LogErrorKey, err)
		return
	}
	slog.InfoContext(ctx, "simulator: cancellation requested",
		slog.String("order_id", string(orderID)), slog.String("outcome", string(result.Outcome)))
}
