// Package cancellationpolicy is the production CancellationPolicy of orders
// (FR-462): the window opens when the Order is placed; an ASAP Order keeps it
// open for a configured duration (a placeholder until the product rule is
// agreed), a Scheduled Order until its Delivery Window starts minus the
// configured cutoff.
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
	window          time.Duration
	scheduledCutoff time.Duration
}

// NewFixed takes CANCELLATION__WINDOW and CANCELLATION__SCHEDULED_CUTOFF; both
// are positive.
func NewFixed(window, scheduledCutoff time.Duration) (Fixed, error) {
	if window <= 0 {
		return Fixed{}, fmt.Errorf("cancellation window must be positive, got %s", window)
	}
	if scheduledCutoff <= 0 {
		return Fixed{}, fmt.Errorf("cancellation scheduled cutoff must be positive, got %s", scheduledCutoff)
	}
	return Fixed{window: window, scheduledCutoff: scheduledCutoff}, nil
}

// Window is computed from the Order data only, so Get Order shows it without
// Temporal: a Scheduled Order closes it at window_start − scheduled cutoff.
func (p Fixed) Window(_ context.Context, placedAt time.Time, delivery order.DeliveryWindow) (order.CancellationWindow, error) {
	if !delivery.IsZero() {
		return order.CancellationWindow{OpensAt: placedAt, ClosesAt: delivery.Start().Add(-p.scheduledCutoff)}, nil
	}
	return order.CancellationWindow{OpensAt: placedAt, ClosesAt: placedAt.Add(p.window)}, nil
}

// Policy is the port the decorator wraps.
type Policy interface {
	Window(ctx context.Context, placedAt time.Time, delivery order.DeliveryWindow) (order.CancellationWindow, error)
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

func (o Observed) Window(ctx context.Context, placedAt time.Time, delivery order.DeliveryWindow) (order.CancellationWindow, error) {
	window, err := o.next.Window(ctx, placedAt, delivery)
	if err != nil {
		trace.SpanFromContext(ctx).RecordError(err)
		o.logger.WarnContext(ctx, "cancellation window is unavailable", slog.Any("error", err))
	}
	return window, err
}
