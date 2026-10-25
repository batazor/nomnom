package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"nomnom/orders/slices/order/domains/order"
)

// Clock is a manually advanced application.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func NewClock(now time.Time) *Clock { return &Clock{now: now} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// IDs generates sequential order identifiers order-1, order-2, ...
type IDs struct {
	mu   sync.Mutex
	next int
}

func (g *IDs) NewOrderID(context.Context) (order.ID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return order.ID(fmt.Sprintf("order-%d", g.next)), nil
}

// AlwaysOpen is a cancellation policy without a window; it is a test stand-in,
// not a product decision.
type AlwaysOpen struct{}

func (AlwaysOpen) Window(context.Context, time.Time) (order.CancellationWindow, error) {
	return order.CancellationWindow{ClosesAt: time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)}, nil
}

// FixedWindow opens the cancellation window at placement for Duration.
type FixedWindow struct {
	Duration time.Duration
}

func (w FixedWindow) Window(_ context.Context, placedAt time.Time) (order.CancellationWindow, error) {
	return order.CancellationWindow{OpensAt: placedAt, ClosesAt: placedAt.Add(w.Duration)}, nil
}
