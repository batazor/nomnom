package applications

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var expiresAt = time.Date(2026, 10, 25, 12, 5, 0, 0, time.UTC)

var offer = Offer{ID: "offer-1", DeliveryID: "delivery-1", ExpiresAt: expiresAt}

func TestAvailableCourierSeesOffers(t *testing.T) {
	couriers := NewMockCouriers(t)
	couriers.EXPECT().State(mock.Anything, "courier-1").Return(CourierState{Availability: AvailabilityAvailable}, nil).Once()
	couriers.EXPECT().Offers(mock.Anything, "courier-1").Return([]Offer{offer}, nil).Once()

	view, err := NewGetCourier(couriers).Handle(context.Background(), "courier-1")

	require.NoError(t, err)
	assert.Equal(t, CourierView{Availability: AvailabilityAvailable, Offers: []Offer{offer}}, view)
}

func TestBusyOrUnavailableCourierHasNoOffers(t *testing.T) {
	delivery := &Delivery{ID: "delivery-1", OrderID: "order-1", Stage: StageAssigned}
	for _, state := range []CourierState{
		{Availability: AvailabilityBusy, CurrentDelivery: delivery},
		{Availability: AvailabilityUnavailable},
	} {
		// Offers is not expected: the offers are not read.
		couriers := NewMockCouriers(t)
		couriers.EXPECT().State(mock.Anything, "courier-1").Return(state, nil).Once()
		view, err := NewGetCourier(couriers).Handle(context.Background(), "courier-1")
		require.NoError(t, err)
		assert.Equal(t, CourierView{Availability: state.Availability, Offers: []Offer{}, CurrentDelivery: state.CurrentDelivery}, view)
	}
}

func TestSetAvailabilityAcceptsAvailableAndUnavailableOnly(t *testing.T) {
	couriers := NewMockCouriers(t)
	couriers.EXPECT().SetAvailability(mock.Anything, "courier-1", AvailabilityUnavailable).Return(nil).Once()
	couriers.EXPECT().State(mock.Anything, "courier-1").Return(CourierState{Availability: AvailabilityUnavailable}, nil).Once()
	h := NewSetAvailability(couriers)

	result, err := h.Handle(context.Background(), "courier-1", AvailabilityUnavailable)
	require.NoError(t, err)
	assert.Equal(t, AvailabilityUnavailable, result.Courier.Availability)
	assert.Empty(t, result.Problem)

	_, err = h.Handle(context.Background(), "courier-1", AvailabilityBusy)
	assert.ErrorIs(t, err, ErrUnknownCommand)

	couriers.EXPECT().SetAvailability(mock.Anything, "courier-1", AvailabilityAvailable).Return(ErrAvailabilityConflict).Once()
	_, err = h.Handle(context.Background(), "courier-1", AvailabilityAvailable)
	assert.ErrorIs(t, err, ErrAvailabilityConflict, "the schema has no problem for it")
}

func TestUnavailableOfferIsAProblemWithTheCurrentCourier(t *testing.T) {
	couriers := NewMockCouriers(t)
	couriers.EXPECT().AcceptOffer(mock.Anything, "courier-1", "offer-1").Return(ErrOfferNotAvailable).Once()
	couriers.EXPECT().State(mock.Anything, "courier-1").Return(CourierState{Availability: AvailabilityAvailable}, nil).Once()
	couriers.EXPECT().Offers(mock.Anything, "courier-1").Return(nil, nil).Once()

	result, err := NewAcceptOffer(couriers).Handle(context.Background(), "courier-1", "offer-1")

	require.NoError(t, err)
	assert.Equal(t, ProblemOfferNotAvailable, result.Problem)
	assert.Equal(t, AvailabilityAvailable, result.Courier.Availability)
}

func TestDeliveryActions(t *testing.T) {
	couriers := NewMockCouriers(t)
	couriers.EXPECT().State(mock.Anything, "courier-1").Return(CourierState{Availability: AvailabilityBusy}, nil).Times(3)
	mock.InOrder(
		couriers.EXPECT().ConfirmPickup(mock.Anything, "courier-1", "delivery-1").Return(nil).Once(),
		couriers.EXPECT().ConfirmDelivery(mock.Anything, "courier-1", "delivery-1").Return(nil).Once(),
	)
	h := NewApplyDeliveryAction(couriers)

	_, err := h.Handle(context.Background(), "courier-1", "delivery-1", ActionConfirmPickup)
	require.NoError(t, err)
	_, err = h.Handle(context.Background(), "courier-1", "delivery-1", ActionConfirmDelivery)
	require.NoError(t, err)

	couriers.EXPECT().ConfirmDelivery(mock.Anything, "courier-1", "delivery-1").Return(ErrStageConflict).Once()
	result, err := h.Handle(context.Background(), "courier-1", "delivery-1", ActionConfirmDelivery)
	require.NoError(t, err)
	assert.Equal(t, ProblemDeliveryStageConflict, result.Problem)

	couriers.EXPECT().ConfirmPickup(mock.Anything, "courier-1", "delivery-1").Return(ErrDeliveryNotFound).Once()
	_, err = h.Handle(context.Background(), "courier-1", "delivery-1", ActionConfirmPickup)
	assert.ErrorIs(t, err, ErrDeliveryNotFound)
	_, err = h.Handle(context.Background(), "courier-1", "delivery-1", "teleport")
	assert.ErrorIs(t, err, ErrUnknownCommand)
}
