package applications

import (
	"context"
	"fmt"
)

// GetCourier builds the courier screen.
type GetCourier struct {
	couriers Couriers
}

func NewGetCourier(couriers Couriers) GetCourier {
	return GetCourier{couriers: couriers}
}

// Handle returns ErrNotCourier for a user who is not a registered courier.
// Offers are read only while the courier is available: delivery offers
// nobody else.
func (h GetCourier) Handle(ctx context.Context, courierID string) (CourierView, error) {
	state, err := h.couriers.State(ctx, courierID)
	if err != nil {
		return CourierView{}, err
	}
	view := CourierView{Availability: state.Availability, Offers: []Offer{}, CurrentDelivery: state.CurrentDelivery}
	if state.Availability != AvailabilityAvailable {
		return view, nil
	}
	view.Offers, err = h.couriers.Offers(ctx, courierID)
	if err != nil {
		return CourierView{}, fmt.Errorf("list offers: %w", err)
	}
	return view, nil
}
