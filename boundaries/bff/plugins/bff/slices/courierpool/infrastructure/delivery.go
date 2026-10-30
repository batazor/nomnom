// Package infrastructure adapts the Courier Pool port to the Courier Pool
// RPCs of delivery.v1.DeliveryService — GetCourierPool, RegisterCourier,
// UpdateCourierLocation, SetCourierAvailability, RetireCourier and
// OfferDeliveryToCourier — called through the internal gateway on behalf of
// the user (ADR 32). delivery authorizes platform#manage_couriers, and
// platform#offer_delivery for the offer.
package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"buf.build/gen/go/nomnom/delivery/grpc/go/nomnom/delivery/v1/deliveryv1grpc"
	deliveryv1 "buf.build/gen/go/nomnom/delivery/protocolbuffers/go/nomnom/delivery/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/courierpool/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/market"
)

// Delivery implements applications.Dispatch.
type Delivery struct {
	client deliveryv1grpc.DeliveryServiceClient
}

func NewDelivery(conn grpc.ClientConnInterface) *Delivery {
	return &Delivery{client: deliveryv1grpc.NewDeliveryServiceClient(conn)}
}

var _ applications.Dispatch = (*Delivery)(nil)

var availabilities = map[deliveryv1.CourierStatus]applications.Availability{
	deliveryv1.CourierStatus_COURIER_STATUS_AVAILABLE:   applications.AvailabilityAvailable,
	deliveryv1.CourierStatus_COURIER_STATUS_BUSY:        applications.AvailabilityBusy,
	deliveryv1.CourierStatus_COURIER_STATUS_UNAVAILABLE: applications.AvailabilityUnavailable,
}

// Pool is the Courier Pool of the Market of the request.
func (d *Delivery) Pool(ctx context.Context) (applications.Pool, error) {
	marketID, err := market.From(ctx)
	if err != nil {
		return applications.Pool{}, fmt.Errorf("get courier pool: %w", err)
	}
	response, err := d.client.GetCourierPool(ctx, &deliveryv1.GetCourierPoolRequest{MarketId: marketID})
	if err != nil {
		return applications.Pool{}, poolError("get courier pool", err, codes.OK, nil)
	}
	couriers := make([]applications.Courier, 0, len(response.GetCouriers()))
	for _, courier := range response.GetCouriers() {
		availability, ok := availabilities[courier.GetStatus()]
		if !ok {
			return applications.Pool{}, fmt.Errorf("courier %s in unexpected status %s", courier.GetCourierId(), courier.GetStatus())
		}
		couriers = append(couriers, applications.Courier{
			ID:             courier.GetCourierId(),
			Name:           courier.GetName(),
			Availability:   availability,
			OfferedOrderID: courier.GetOfferedOrderId(),
			CurrentOrderID: courier.GetActiveOrderId(),
		})
	}
	return applications.Pool{
		Market:   marketID,
		Size:     int(response.GetSize()),
		OnLine:   int(response.GetOnLine()),
		Busy:     int(response.GetBusy()),
		Waiting:  int(response.GetWaiting()),
		Couriers: couriers,
	}, nil
}

// Register adds the courier to the Courier Pool of the Market of the request.
func (d *Delivery) Register(ctx context.Context, courierID, name string) error {
	marketID, err := market.From(ctx)
	if err != nil {
		return fmt.Errorf("register courier: %w", err)
	}
	_, err = d.client.RegisterCourier(ctx, &deliveryv1.RegisterCourierRequest{CourierId: courierID, Name: name, MarketId: marketID})
	return poolError("register courier", err, codes.InvalidArgument, applications.ErrInvalidCourierName)
}

func (d *Delivery) Place(ctx context.Context, courierID string, location applications.Location) error {
	_, err := d.client.UpdateCourierLocation(ctx, &deliveryv1.UpdateCourierLocationRequest{
		CourierId: courierID,
		Latitude:  location.Latitude,
		Longitude: location.Longitude,
	})
	return poolError("update courier location", err, codes.OK, nil)
}

func (d *Delivery) SetOnLine(ctx context.Context, courierID string, onLine bool) error {
	availability := deliveryv1.CourierAvailability_COURIER_AVAILABILITY_UNAVAILABLE
	if onLine {
		availability = deliveryv1.CourierAvailability_COURIER_AVAILABILITY_AVAILABLE
	}
	_, err := d.client.SetCourierAvailability(ctx, &deliveryv1.SetCourierAvailabilityRequest{CourierId: courierID, Availability: availability})
	return poolError("set courier availability", err, codes.FailedPrecondition, applications.ErrStatusConflict)
}

func (d *Delivery) Retire(ctx context.Context, courierID string) error {
	_, err := d.client.RetireCourier(ctx, &deliveryv1.RetireCourierRequest{CourierId: courierID})
	return poolError("retire courier", err, codes.FailedPrecondition, applications.ErrCourierOnLine)
}

// Offer offers the delivery of the order to the courier. An order without a
// delivery and an unknown courier are refusals of the offer, as are a
// delivery that waits for no courier and a courier who is not free.
func (d *Delivery) Offer(ctx context.Context, orderID, courierID string) error {
	_, err := d.client.OfferDeliveryToCourier(ctx, &deliveryv1.OfferDeliveryToCourierRequest{OrderId: orderID, CourierId: courierID})
	switch status.Code(err) {
	case codes.NotFound, codes.FailedPrecondition:
		return applications.ErrOfferRefused
	}
	return poolError("offer delivery to courier", err, codes.OK, nil)
}

// poolError names the refusals of delivery: refusal is the status of the one
// business refusal of the call (codes.OK when it has none), refused what it
// means.
func poolError(operation string, err error, refusal codes.Code, refused error) error {
	code := status.Code(err)
	switch {
	case code == codes.OK:
		return nil
	case code == codes.PermissionDenied:
		return applications.ErrNotPermitted
	case code == codes.NotFound:
		return applications.ErrCourierNotFound
	case refusal != codes.OK && code == refusal:
		return refused
	}
	return fmt.Errorf("%s: %w", operation, err)
}
