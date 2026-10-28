package order

import (
	"errors"
	"fmt"
	"time"

	"nomnom/orders/slices/order/domains/order/vo"
)

// ErrInvalidDeliveryWindow: a Delivery Window needs both bounds and a start
// before its end.
var ErrInvalidDeliveryWindow = fmt.Errorf("%w: delivery window requires start before end", vo.ErrInvalidValue)

// ErrNotScheduled: the operation exists only for a Scheduled Order.
var ErrNotScheduled = errors.New("order has no delivery window")

// DeliveryWindow is the half-open interval [Start, End) in which the Customer
// expects a Scheduled Order (FR-681). The zero value is an ASAP Order. Both
// bounds are UTC instants: restaurants fixes them at the slot reservation.
type DeliveryWindow struct {
	start time.Time
	end   time.Time
}

// NewDeliveryWindow accepts only a non-empty interval.
func NewDeliveryWindow(start, end time.Time) (DeliveryWindow, error) {
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return DeliveryWindow{}, fmt.Errorf("%w: [%s, %s)", ErrInvalidDeliveryWindow, start, end)
	}
	return DeliveryWindow{start: start.UTC(), end: end.UTC()}, nil
}

func (w DeliveryWindow) Start() time.Time { return w.start }
func (w DeliveryWindow) End() time.Time   { return w.end }

// IsZero reports an ASAP Order.
func (w DeliveryWindow) IsZero() bool { return w.start.IsZero() }

// DeliveryWindow is the window of a Scheduled Order; zero for an ASAP Order.
func (o *Order) DeliveryWindow() DeliveryWindow { return o.deliveryWindow }

// Scheduled reports an Order with a Delivery Window.
func (o *Order) Scheduled() bool { return !o.deliveryWindow.IsZero() }
