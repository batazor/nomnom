package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

// fakeCourierScenarios records the courier id each scenario got.
type fakeCourierScenarios struct {
	view    applications.CourierView
	result  applications.CommandResult
	err     error
	calls   []string
	courier string
}

func (f *fakeCourierScenarios) record(courierID, call string) {
	f.courier = courierID
	f.calls = append(f.calls, call)
}

type getCourierFunc struct{ *fakeCourierScenarios }

func (f getCourierFunc) Handle(_ context.Context, courierID string) (applications.CourierView, error) {
	f.record(courierID, "get")
	return f.view, f.err
}

type setAvailabilityFunc struct{ *fakeCourierScenarios }

func (f setAvailabilityFunc) Handle(_ context.Context, courierID string, availability applications.Availability) (applications.CommandResult, error) {
	f.record(courierID, "availability "+string(availability))
	return f.result, f.err
}

type acceptOfferFunc struct{ *fakeCourierScenarios }

func (f acceptOfferFunc) Handle(_ context.Context, courierID, offerID string) (applications.CommandResult, error) {
	f.record(courierID, "accept "+offerID)
	return f.result, f.err
}

type deliveryActionFunc struct{ *fakeCourierScenarios }

func (f deliveryActionFunc) Handle(_ context.Context, courierID, deliveryID string, action applications.Action) (applications.CommandResult, error) {
	f.record(courierID, string(action)+" "+deliveryID)
	return f.result, f.err
}

func courierService(t *testing.T, fake *fakeCourierScenarios) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{accessTokens: verifier, courier: &courier{
		getCourier:      getCourierFunc{fake},
		setAvailability: setAvailabilityFunc{fake},
		acceptOffer:     acceptOfferFunc{fake},
		applyAction:     deliveryActionFunc{fake},
	}}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

var busyCourier = applications.CourierView{
	Availability: applications.AvailabilityBusy,
	Offers:       []applications.Offer{},
	CurrentDelivery: &applications.Delivery{
		ID: "delivery-1", OrderID: "order-1", Stage: applications.StagePickedUp,
		PickupAddress: "ул. Садовая, 8", DropoffAddress: "ул. Лесная, 12",
	},
}

func TestCourierIsTheVerifiedUser(t *testing.T) {
	fake := &fakeCourierScenarios{view: applications.CourierView{
		Availability: applications.AvailabilityAvailable,
		Offers: []applications.Offer{{
			ID: "offer-1", DeliveryID: "delivery-1", OrderID: "order-1", PickupAddress: "a", DropoffAddress: "b",
			ExpiresAt: time.Date(2026, 10, 25, 12, 5, 0, 0, time.UTC),
		}},
	}}
	svc, ctx := courierService(t, fake)

	response, err := svc.QueryCourier(ctx, &service.QueryCourierRequest{})

	require.NoError(t, err)
	assert.NotEmpty(t, fake.courier, "the courier id is the subject of the access token")
	courierMessage := response.GetCourier()
	assert.Equal(t, service.CourierAvailability_COURIER_AVAILABILITY_AVAILABLE, courierMessage.GetAvailability())
	require.Len(t, courierMessage.GetOffers(), 1)
	assert.Equal(t, "offer-1", courierMessage.GetOffers()[0].GetId())
	assert.Equal(t, "order-1", courierMessage.GetOffers()[0].GetOrderId())
	assert.Equal(t, "2026-10-25T12:05:00Z", courierMessage.GetOffers()[0].GetExpiresAt())
	assert.Nil(t, courierMessage.GetCurrentDelivery())

	fake.view = busyCourier
	response, err = svc.QueryCourier(ctx, &service.QueryCourierRequest{})
	require.NoError(t, err)
	delivery := response.GetCourier().GetCurrentDelivery()
	assert.Equal(t, service.CourierAvailability_COURIER_AVAILABILITY_BUSY, response.GetCourier().GetAvailability())
	assert.Equal(t, service.CourierDeliveryStage_COURIER_DELIVERY_STAGE_PICKED_UP, delivery.GetStage())
	assert.Equal(t, "ул. Лесная, 12", delivery.GetDropoffAddress())
	assert.Equal(t, "order-1", delivery.GetOrderId())
}

func TestNotACourierIsNull(t *testing.T) {
	svc, ctx := courierService(t, &fakeCourierScenarios{err: applications.ErrNotCourier})
	response, err := svc.QueryCourier(ctx, &service.QueryCourierRequest{})
	require.NoError(t, err)
	assert.Nil(t, response.GetCourier())

	_, err = svc.QueryCourier(context.Background(), &service.QueryCourierRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestCourierCommands(t *testing.T) {
	fake := &fakeCourierScenarios{result: applications.CommandResult{Courier: busyCourier}}
	svc, ctx := courierService(t, fake)

	availability, err := svc.MutationSetCourierAvailability(ctx, &service.MutationSetCourierAvailabilityRequest{Availability: service.CourierAvailability_COURIER_AVAILABILITY_UNAVAILABLE})
	require.NoError(t, err)
	assert.Equal(t, service.CourierAvailability_COURIER_AVAILABILITY_BUSY, availability.GetSetCourierAvailability().GetCourier().GetAvailability())

	fake.result.Problem = applications.ProblemOfferNotAvailable
	accepted, err := svc.MutationAcceptDeliveryOffer(ctx, &service.MutationAcceptDeliveryOfferRequest{OfferId: "offer-1"})
	require.NoError(t, err)
	assert.Equal(t, service.CourierProblemCode_COURIER_PROBLEM_CODE_OFFER_NOT_AVAILABLE, accepted.GetAcceptDeliveryOffer().GetProblem())
	assert.NotNil(t, accepted.GetAcceptDeliveryOffer().GetCourier())

	fake.result.Problem = applications.ProblemDeliveryStageConflict
	applied, err := svc.MutationApplyCourierDeliveryAction(ctx, &service.MutationApplyCourierDeliveryActionRequest{DeliveryId: "delivery-1", Action: service.CourierDeliveryAction_COURIER_DELIVERY_ACTION_CONFIRM_DELIVERY})
	require.NoError(t, err)
	assert.Equal(t, service.CourierProblemCode_COURIER_PROBLEM_CODE_DELIVERY_STAGE_CONFLICT, applied.GetApplyCourierDeliveryAction().GetProblem())

	assert.Equal(t, []string{"availability unavailable", "accept offer-1", "confirm_delivery delivery-1"}, fake.calls)
}

func TestCourierCommandValidation(t *testing.T) {
	fake := &fakeCourierScenarios{}
	svc, ctx := courierService(t, fake)
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
	assert.Empty(t, fake.calls)
}

func TestCourierCommandErrors(t *testing.T) {
	for refusal, code := range map[error]codes.Code{
		applications.ErrNotCourier:           codes.PermissionDenied,
		applications.ErrDeliveryNotFound:     codes.NotFound,
		applications.ErrAvailabilityConflict: codes.FailedPrecondition,
		applications.ErrInvalidCommand:       codes.InvalidArgument,
		errors.New("down"):                   codes.Unavailable,
	} {
		svc, ctx := courierService(t, &fakeCourierScenarios{err: refusal})
		_, err := svc.MutationApplyCourierDeliveryAction(ctx, &service.MutationApplyCourierDeliveryActionRequest{DeliveryId: "delivery-1", Action: service.CourierDeliveryAction_COURIER_DELIVERY_ACTION_CONFIRM_PICKUP})
		assert.Equal(t, code, status.Code(err), refusal.Error())
	}
}
