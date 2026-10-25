package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
)

// courierSlice names the slice in "not available" errors.
const courierSlice = "courier"

// requireCourier authenticates the user and returns the courier id: delivery
// registers a courier under the user id of that user.
func (s *BffService) requireCourier(ctx context.Context) (context.Context, string, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, "", err
	}
	if err := requireSlice(courierSlice, s.courier != nil); err != nil {
		return nil, "", err
	}
	user, _ := identity.UserFrom(ctx)
	return ctx, user.ID, nil
}

// QueryCourier shows the work of the verified user as a courier; a user who is
// not a registered courier gets null.
func (s *BffService) QueryCourier(ctx context.Context, _ *service.QueryCourierRequest) (*service.QueryCourierResponse, error) {
	ctx, courierID, err := s.requireCourier(ctx)
	if err != nil {
		return nil, err
	}
	view, err := s.courier.getCourier.Handle(ctx, courierID)
	if errors.Is(err, applications.ErrNotCourier) {
		return &service.QueryCourierResponse{}, nil
	}
	if err != nil {
		return nil, internalError("courier", err)
	}
	message, err := courierMessage(view)
	if err != nil {
		return nil, internalError("courier", err)
	}
	return &service.QueryCourierResponse{Courier: message}, nil
}

// MutationSetCourierAvailability accepts AVAILABLE or UNAVAILABLE only: BUSY is
// set by an assignment.
func (s *BffService) MutationSetCourierAvailability(ctx context.Context, request *service.MutationSetCourierAvailabilityRequest) (*service.MutationSetCourierAvailabilityResponse, error) {
	availability, ok := requestedCourierAvailabilities[request.GetAvailability()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "availability must be AVAILABLE or UNAVAILABLE")
	}
	ctx, courierID, err := s.requireCourier(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.courier.setAvailability.Handle(ctx, courierID, availability)
	payload, err := courierPayload("setCourierAvailability", result, err)
	if err != nil {
		return nil, err
	}
	return &service.MutationSetCourierAvailabilityResponse{SetCourierAvailability: payload}, nil
}

func (s *BffService) MutationAcceptDeliveryOffer(ctx context.Context, request *service.MutationAcceptDeliveryOfferRequest) (*service.MutationAcceptDeliveryOfferResponse, error) {
	if request.GetOfferId() == "" {
		return nil, status.Error(codes.InvalidArgument, "offerId is required")
	}
	ctx, courierID, err := s.requireCourier(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.courier.acceptOffer.Handle(ctx, courierID, request.GetOfferId())
	payload, err := courierPayload("acceptDeliveryOffer", result, err)
	if err != nil {
		return nil, err
	}
	return &service.MutationAcceptDeliveryOfferResponse{AcceptDeliveryOffer: payload}, nil
}

func (s *BffService) MutationApplyCourierDeliveryAction(ctx context.Context, request *service.MutationApplyCourierDeliveryActionRequest) (*service.MutationApplyCourierDeliveryActionResponse, error) {
	if request.GetDeliveryId() == "" {
		return nil, status.Error(codes.InvalidArgument, "deliveryId is required")
	}
	action, ok := courierDeliveryActions[request.GetAction()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "action is required")
	}
	ctx, courierID, err := s.requireCourier(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.courier.applyAction.Handle(ctx, courierID, request.GetDeliveryId(), action)
	payload, err := courierPayload("applyCourierDeliveryAction", result, err)
	if err != nil {
		return nil, err
	}
	return &service.MutationApplyCourierDeliveryActionResponse{ApplyCourierDeliveryAction: payload}, nil
}

// courierPayload translates the refusals of delivery that the schema has no
// problem code for into GraphQL errors.
func courierPayload(operation string, result applications.CommandResult, err error) (*service.CourierPayload, error) {
	switch {
	case errors.Is(err, applications.ErrNotCourier):
		return nil, status.Error(codes.PermissionDenied, "user is not a registered courier")
	case errors.Is(err, applications.ErrDeliveryNotFound):
		return nil, status.Error(codes.NotFound, "delivery not found")
	case errors.Is(err, applications.ErrAvailabilityConflict):
		return nil, status.Error(codes.FailedPrecondition, "courier status forbids the availability change")
	case errors.Is(err, applications.ErrInvalidCommand):
		return nil, status.Error(codes.InvalidArgument, "courier command is invalid")
	case err != nil:
		return nil, internalError(operation, err)
	}
	payload, err := courierCommandPayload(result)
	if err != nil {
		return nil, internalError(operation, err)
	}
	return payload, nil
}
