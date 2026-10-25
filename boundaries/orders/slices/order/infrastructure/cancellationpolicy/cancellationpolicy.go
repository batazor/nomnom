// Package cancellationpolicy is the production CancellationPolicy of orders:
// the window opens when the Order is placed and lasts a configured duration.
// The duration is a placeholder until the product rule is agreed.
package cancellationpolicy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/trace"

	"nomnom/orders/slices/order/domains/order"
)

type Fixed struct {
	window time.Duration
}

func NewFixed(window time.Duration) (Fixed, error) {
	if window <= 0 {
		return Fixed{}, fmt.Errorf("cancellation window must be positive, got %s", window)
	}
	return Fixed{window: window}, nil
}

func (p Fixed) Window(_ context.Context, placedAt time.Time) (order.CancellationWindow, error) {
	return order.CancellationWindow{OpensAt: placedAt, ClosesAt: placedAt.Add(p.window)}, nil
}

// Policy is the port the decorator wraps.
type Policy interface {
	Window(ctx context.Context, placedAt time.Time) (order.CancellationWindow, error)
}

// Observed reports a failing policy on the log and on the current span. Get
// Order hides the window on such a failure instead of failing the read, so
// the failure would be silent without this decorator.
type Observed struct {
	next   Policy
	logger *slog.Logger
}

func Observe(next Policy, logger *slog.Logger) Observed {
	return Observed{next: next, logger: logger}
}

func (o Observed) Window(ctx context.Context, placedAt time.Time) (order.CancellationWindow, error) {
	window, err := o.next.Window(ctx, placedAt)
	if err != nil {
		trace.SpanFromContext(ctx).RecordError(err)
		o.logger.WarnContext(ctx, "cancellation window is unavailable", slog.Any("error", err))
	}
	return window, err
}
