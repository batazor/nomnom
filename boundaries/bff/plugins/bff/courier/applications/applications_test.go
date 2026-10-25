package applications

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expiresAt = time.Date(2026, 10, 25, 12, 5, 0, 0, time.UTC)

type fakeCouriers struct {
	state      CourierState
	stateErr   error
	offers     []Offer
	offerReads int
	commands   []string
	commandErr error
}

func (f *fakeCouriers) State(context.Context, string) (CourierState, error) {
	return f.state, f.stateErr
}

func (f *fakeCouriers) Offers(context.Context, string) ([]Offer, error) {
	f.offerReads++
	return f.offers, nil
}

func (f *fakeCouriers) command(name string) error {
	f.commands = append(f.commands, name)
	return f.commandErr
}

func (f *fakeCouriers) SetAvailability(_ context.Context, courierID string, availability Availability) error {
	return f.command(courierID + " availability " + string(availability))
}

func (f *fakeCouriers) AcceptOffer(_ context.Context, courierID, offerID string) error {
	return f.command(courierID + " accept " + offerID)
}

func (f *fakeCouriers) ConfirmPickup(_ context.Context, courierID, deliveryID string) error {
	return f.command(courierID + " pickup " + deliveryID)
}

func (f *fakeCouriers) ConfirmDelivery(_ context.Context, courierID, deliveryID string) error {
	return f.command(courierID + " delivery " + deliveryID)
}

var offer = Offer{ID: "offer-1", DeliveryID: "delivery-1", PickupAddress: "ул. Садовая, 8", ExpiresAt: expiresAt}

func TestAvailableCourierSeesOffers(t *testing.T) {
	couriers := &fakeCouriers{state: CourierState{Availability: AvailabilityAvailable}, offers: []Offer{offer}}

	view, err := NewGetCourier(couriers).Handle(context.Background(), "courier-1")

	require.NoError(t, err)
	assert.Equal(t, CourierView{Availability: AvailabilityAvailable, Offers: []Offer{offer}}, view)
}

func TestBusyOrUnavailableCourierHasNoOffers(t *testing.T) {
	delivery := &Delivery{ID: "delivery-1", OrderID: "order-1", Stage: StageAssigned, PickupAddress: "a"}
	for _, state := range []CourierState{
		{Availability: AvailabilityBusy, CurrentDelivery: delivery},
		{Availability: AvailabilityUnavailable},
	} {
		couriers := &fakeCouriers{state: state, offers: []Offer{offer}}
		view, err := NewGetCourier(couriers).Handle(context.Background(), "courier-1")
		require.NoError(t, err)
		assert.Equal(t, CourierView{Availability: state.Availability, Offers: []Offer{}, CurrentDelivery: state.CurrentDelivery}, view)
		assert.Zero(t, couriers.offerReads)
	}
}

func TestSetAvailabilityAcceptsAvailableAndUnavailableOnly(t *testing.T) {
	couriers := &fakeCouriers{state: CourierState{Availability: AvailabilityUnavailable}}
	h := NewSetAvailability(couriers)

	result, err := h.Handle(context.Background(), "courier-1", AvailabilityUnavailable)
	require.NoError(t, err)
	assert.Equal(t, AvailabilityUnavailable, result.Courier.Availability)
	assert.Empty(t, result.Problem)

	_, err = h.Handle(context.Background(), "courier-1", AvailabilityBusy)
	assert.ErrorIs(t, err, ErrUnknownCommand)
	assert.Equal(t, []string{"courier-1 availability unavailable"}, couriers.commands)

	couriers.commandErr = ErrAvailabilityConflict
	_, err = h.Handle(context.Background(), "courier-1", AvailabilityAvailable)
	assert.ErrorIs(t, err, ErrAvailabilityConflict, "the schema has no problem for it")
}

func TestUnavailableOfferIsAProblemWithTheCurrentCourier(t *testing.T) {
	couriers := &fakeCouriers{state: CourierState{Availability: AvailabilityAvailable}, commandErr: ErrOfferNotAvailable}

	result, err := NewAcceptOffer(couriers).Handle(context.Background(), "courier-1", "offer-1")

	require.NoError(t, err)
	assert.Equal(t, ProblemOfferNotAvailable, result.Problem)
	assert.Equal(t, AvailabilityAvailable, result.Courier.Availability)
	assert.Equal(t, []string{"courier-1 accept offer-1"}, couriers.commands)
}

func TestDeliveryActions(t *testing.T) {
	couriers := &fakeCouriers{state: CourierState{Availability: AvailabilityBusy}}
	h := NewApplyDeliveryAction(couriers)

	_, err := h.Handle(context.Background(), "courier-1", "delivery-1", ActionConfirmPickup)
	require.NoError(t, err)
	_, err = h.Handle(context.Background(), "courier-1", "delivery-1", ActionConfirmDelivery)
	require.NoError(t, err)
	assert.Equal(t, []string{"courier-1 pickup delivery-1", "courier-1 delivery delivery-1"}, couriers.commands)

	couriers.commandErr = ErrStageConflict
	result, err := h.Handle(context.Background(), "courier-1", "delivery-1", ActionConfirmDelivery)
	require.NoError(t, err)
	assert.Equal(t, ProblemDeliveryStageConflict, result.Problem)

	couriers.commandErr = ErrDeliveryNotFound
	_, err = h.Handle(context.Background(), "courier-1", "delivery-1", ActionConfirmPickup)
	assert.ErrorIs(t, err, ErrDeliveryNotFound)
	_, err = h.Handle(context.Background(), "courier-1", "delivery-1", "teleport")
	assert.ErrorIs(t, err, ErrUnknownCommand)
}
