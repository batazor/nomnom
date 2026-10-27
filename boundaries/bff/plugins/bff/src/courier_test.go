package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

// courierScenarios are the courier scenarios the service calls.
type courierScenarios struct {
	getCourier      *mockGetCourierHandler
	setAvailability *mockSetAvailabilityHandler
	acceptOffer     *mockAcceptOfferHandler
	applyAction     *mockDeliveryActionHandler
}

func newCourierScenarios(t *testing.T) courierScenarios {
	t.Helper()
	return courierScenarios{
		getCourier:      newMockGetCourierHandler(t),
		setAvailability: newMockSetAvailabilityHandler(t),
		acceptOffer:     newMockAcceptOfferHandler(t),
		applyAction:     newMockDeliveryActionHandler(t),
	}
}

// verifiedCourier matches the courier id taken from the access token.
func verifiedCourier() any {
	return mock.MatchedBy(func(courierID string) bool { return courierID != "" })
}

func courierService(t *testing.T, scenarios courierScenarios) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{accessTokens: verifier, courier: &courier{
		getCourier:      scenarios.getCourier,
		setAvailability: scenarios.setAvailability,
		acceptOffer:     scenarios.acceptOffer,
		applyAction:     scenarios.applyAction,
	}}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

var busyCourier = applications.CourierView{
	Availability: applications.AvailabilityBusy,
	Offers:       []applications.Offer{},
	CurrentDelivery: &applications.Delivery{
		ID: "delivery-1", OrderID: "order-1", Stage: applications.StagePickedUp,
	},
}

func TestCourierIsTheVerifiedUser(t *testing.T) {
	scenarios := newCourierScenarios(t)
	// the courier id is the subject of the access token.
	scenarios.getCourier.EXPECT().Handle(mock.Anything, verifiedCourier()).Return(applications.CourierView{
		Availability: applications.AvailabilityAvailable,
		Offers: []applications.Offer{{
			ID: "offer-1", DeliveryID: "delivery-1", OrderID: "order-1",
			ExpiresAt: time.Date(2026, 10, 25, 12, 5, 0, 0, time.UTC),
		}},
	}, nil).Once()
	scenarios.getCourier.EXPECT().Handle(mock.Anything, verifiedCourier()).Return(busyCourier, nil).Once()
	svc, ctx := courierService(t, scenarios)

	response, err := svc.QueryCourier(ctx, &service.QueryCourierRequest{})

	require.NoError(t, err)
	courierMessage := response.GetCourier()
	assert.Equal(t, service.CourierAvailability_COURIER_AVAILABILITY_AVAILABLE, courierMessage.GetAvailability())
	require.Len(t, courierMessage.GetOffers(), 1)
	assert.Equal(t, "offer-1", courierMessage.GetOffers()[0].GetId())
	assert.Equal(t, "order-1", courierMessage.GetOffers()[0].GetOrderId())
	assert.Equal(t, "2026-10-25T12:05:00Z", courierMessage.GetOffers()[0].GetExpiresAt())
	assert.Nil(t, courierMessage.GetCurrentDelivery())

	response, err = svc.QueryCourier(ctx, &service.QueryCourierRequest{})
	require.NoError(t, err)
	delivery := response.GetCourier().GetCurrentDelivery()
	assert.Equal(t, service.CourierAvailability_COURIER_AVAILABILITY_BUSY, response.GetCourier().GetAvailability())
	assert.Equal(t, service.CourierDeliveryStage_COURIER_DELIVERY_STAGE_PICKED_UP, delivery.GetStage())
	assert.Equal(t, "order-1", delivery.GetOrderId())
}

func TestNotACourierIsNull(t *testing.T) {
	scenarios := newCourierScenarios(t)
	scenarios.getCourier.EXPECT().Handle(mock.Anything, verifiedCourier()).Return(applications.CourierView{}, applications.ErrNotCourier).Once()
	svc, ctx := courierService(t, scenarios)
	response, err := svc.QueryCourier(ctx, &service.QueryCourierRequest{})
	require.NoError(t, err)
	assert.Nil(t, response.GetCourier())

	_, err = svc.QueryCourier(context.Background(), &service.QueryCourierRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestCourierCommands(t *testing.T) {
	scenarios := newCourierScenarios(t)
	mock.InOrder(
		scenarios.setAvailability.EXPECT().Handle(mock.Anything, verifiedCourier(), applications.AvailabilityUnavailable).
			Return(applications.CommandResult{Courier: busyCourier}, nil).Once(),
		scenarios.acceptOffer.EXPECT().Handle(mock.Anything, verifiedCourier(), "offer-1").
			Return(applications.CommandResult{Courier: busyCourier, Problem: applications.ProblemOfferNotAvailable}, nil).Once(),
		scenarios.applyAction.EXPECT().Handle(mock.Anything, verifiedCourier(), "delivery-1", applications.ActionConfirmDelivery).
			Return(applications.CommandResult{Courier: busyCourier, Problem: applications.ProblemDeliveryStageConflict}, nil).Once(),
	)
	svc, ctx := courierService(t, scenarios)

	availability, err := svc.MutationSetCourierAvailability(ctx, &service.MutationSetCourierAvailabilityRequest{Availability: service.CourierAvailability_COURIER_AVAILABILITY_UNAVAILABLE})
	require.NoError(t, err)
	assert.Equal(t, service.CourierAvailability_COURIER_AVAILABILITY_BUSY, availability.GetSetCourierAvailability().GetCourier().GetAvailability())

	accepted, err := svc.MutationAcceptDeliveryOffer(ctx, &service.MutationAcceptDeliveryOfferRequest{OfferId: "offer-1"})
	require.NoError(t, err)
	assert.Equal(t, service.CourierProblemCode_COURIER_PROBLEM_CODE_OFFER_NOT_AVAILABLE, accepted.GetAcceptDeliveryOffer().GetProblem())
	assert.NotNil(t, accepted.GetAcceptDeliveryOffer().GetCourier())

	applied, err := svc.MutationApplyCourierDeliveryAction(ctx, &service.MutationApplyCourierDeliveryActionRequest{DeliveryId: "delivery-1", Action: service.CourierDeliveryAction_COURIER_DELIVERY_ACTION_CONFIRM_DELIVERY})
	require.NoError(t, err)
	assert.Equal(t, service.CourierProblemCode_COURIER_PROBLEM_CODE_DELIVERY_STAGE_CONFLICT, applied.GetApplyCourierDeliveryAction().GetProblem())
}

func TestCourierCommandValidation(t *testing.T) {
	svc, ctx := courierService(t, newCourierScenarios(t))
	for _, availability := range []service.CourierAvailability{service.CourierAvailability_COURIER_AVAILABILITY_BUSY, service.CourierAvailability_COURIER_AVAILABILITY_UNSPECIFIED} {
		_, err := svc.MutationSetCourierAvailability(ctx, &service.MutationSetCourierAvailabilityRequest{Availability: availability})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}
	_, err := svc.MutationAcceptDeliveryOffer(ctx, &service.MutationAcceptDeliveryOfferRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = svc.MutationApplyCourierDeliveryAction(ctx, &service.MutationApplyCourierDeliveryActionRequest{DeliveryId: "delivery-1"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = svc.MutationApplyCourierDeliveryAction(ctx, &service.MutationApplyCourierDeliveryActionRequest{Action: service.CourierDeliveryAction_COURIER_DELIVERY_ACTION_CONFIRM_PICKUP})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCourierCommandErrors(t *testing.T) {
	for refusal, code := range map[error]codes.Code{
		applications.ErrNotCourier:           codes.PermissionDenied,
		applications.ErrDeliveryNotFound:     codes.NotFound,
		applications.ErrAvailabilityConflict: codes.FailedPrecondition,
		applications.ErrInvalidCommand:       codes.InvalidArgument,
		errors.New("down"):                   codes.Unavailable,
	} {
		scenarios := newCourierScenarios(t)
		scenarios.applyAction.EXPECT().Handle(mock.Anything, verifiedCourier(), "delivery-1", applications.ActionConfirmPickup).
			Return(applications.CommandResult{}, refusal).Once()
		svc, ctx := courierService(t, scenarios)
		_, err := svc.MutationApplyCourierDeliveryAction(ctx, &service.MutationApplyCourierDeliveryActionRequest{DeliveryId: "delivery-1", Action: service.CourierDeliveryAction_COURIER_DELIVERY_ACTION_CONFIRM_PICKUP})
		assert.Equal(t, code, status.Code(err), refusal.Error())
	}
}
