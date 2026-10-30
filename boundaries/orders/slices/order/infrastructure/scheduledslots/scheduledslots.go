// Package scheduledslots implements the placeorder.SlotReservations port on
// ReserveScheduledSlot of restaurants, called through the internal gateway
// under the service identity of orders before the Place Order transaction
// (FR-681, FR-692).
package scheduledslots

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"

	"buf.build/gen/go/nomnom/restaurants/grpc/go/nomnom/restaurants/v1/restaurantsv1grpc"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
)

// ReasonScheduledOrdersUnavailable is the ErrorInfo reason of the
// FAILED_PRECONDITION of a restaurant without scheduled order settings or not
// accepting orders now.
const ReasonScheduledOrdersUnavailable = "SCHEDULED_ORDERS_UNAVAILABLE"

// ErrInvalidReservation means restaurants answered outside its contract, such
// as a reservation without a window or an unknown precondition.
var ErrInvalidReservation = errors.New("slot reservation violates the restaurants contract")

// Slots reserves Delivery Windows of restaurants.
type Slots struct {
	client  restaurantsv1grpc.RestaurantsServiceClient
	timeout time.Duration
}

var _ placeorder.SlotReservations = (*Slots)(nil)

// New binds the adapter to the gateway connection; timeout bounds one
// reservation.
func New(conn grpc.ClientConnInterface, timeout time.Duration) *Slots {
	return &Slots{client: restaurantsv1grpc.NewRestaurantsServiceClient(conn), timeout: timeout}
}

// Reserve holds the window under the order id; a repeat with the same order
// id returns the same reservation. A full window is placeorder.SlotUnavailable
// with the alternatives of restaurants.
func (s *Slots) Reserve(ctx context.Context, restaurantID order.RestaurantID, windowStart time.Time, orderID order.ID) (order.DeliveryWindow, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	resp, err := s.client.ReserveScheduledSlot(ctx, &restaurantsv1.ReserveScheduledSlotRequest{
		RestaurantId: string(restaurantID),
		WindowStart:  timestamppb.New(windowStart),
		OrderId:      string(orderID),
	})
	if err != nil {
		return order.DeliveryWindow{}, mapError(restaurantID, err)
	}
	switch result := resp.GetResult().(type) {
	case *restaurantsv1.ReserveScheduledSlotResponse_Reserved:
		return window(result.Reserved)
	case *restaurantsv1.ReserveScheduledSlotResponse_SlotUnavailable:
		alternatives := make([]order.DeliveryWindow, 0, len(result.SlotUnavailable.GetAlternatives()))
		for _, alternative := range result.SlotUnavailable.GetAlternatives() {
			converted, err := window(alternative)
			if err != nil {
				return order.DeliveryWindow{}, err
			}
			alternatives = append(alternatives, converted)
		}
		return order.DeliveryWindow{}, placeorder.SlotUnavailable{Alternatives: alternatives}
	default:
		return order.DeliveryWindow{}, fmt.Errorf("restaurants.ReserveScheduledSlot %q: %w: no result", restaurantID, ErrInvalidReservation)
	}
}

func window(w *restaurantsv1.DeliveryWindow) (order.DeliveryWindow, error) {
	if w.GetStart() == nil || w.GetEnd() == nil {
		return order.DeliveryWindow{}, fmt.Errorf("%w: window without bounds", ErrInvalidReservation)
	}
	converted, err := order.NewDeliveryWindow(w.GetStart().AsTime(), w.GetEnd().AsTime())
	if err != nil {
		return order.DeliveryWindow{}, fmt.Errorf("%w: %w", ErrInvalidReservation, err)
	}
	return converted, nil
}

// mapError: INVALID_ARGUMENT (window off the grid, the horizon or the
// schedule) is invalid input of Place Order, NOT_FOUND an unknown restaurant,
// FAILED_PRECONDITION with reason SCHEDULED_ORDERS_UNAVAILABLE its refusal;
// another precondition breaks the contract. Transport, deadline and identity
// failures are a retryable unavailable dependency; cancellation by the caller
// is returned as is.
func mapError(restaurantID order.RestaurantID, err error) error {
	const call = "restaurants.ReserveScheduledSlot"
	switch code := status.Code(err); {
	case code == codes.InvalidArgument:
		return fmt.Errorf("%s %q: %w: %w", call, restaurantID, usecase.ErrInvalidInput, err)
	case code == codes.NotFound:
		return fmt.Errorf("%s %q: %w: %w", call, restaurantID, placeorder.ErrUnknownRestaurant, err)
	case code == codes.FailedPrecondition && reason(err) == ReasonScheduledOrdersUnavailable:
		return fmt.Errorf("%s %q: %w: %w", call, restaurantID, placeorder.ErrScheduledOrdersUnavailable, err)
	case code == codes.FailedPrecondition:
		return fmt.Errorf("%s %q: %w: %w", call, restaurantID, ErrInvalidReservation, err)
	case code == codes.Canceled || errors.Is(err, context.Canceled):
		return fmt.Errorf("%s %q: %w", call, restaurantID, err)
	default:
		return fmt.Errorf("%s %q: %w: %w", call, restaurantID, usecase.ErrDependencyUnavailable, err)
	}
}

// reason is the ErrorInfo reason of a status; empty without ErrorInfo.
func reason(err error) string {
	for _, detail := range status.Convert(err).Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info.GetReason()
		}
	}
	return ""
}
