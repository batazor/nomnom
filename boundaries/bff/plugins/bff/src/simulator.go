package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/simulator/applications"
)

type getSimulatorHandler interface {
	Handle(ctx context.Context) (bool, error)
}

type setSimulatorEnabledHandler interface {
	Handle(ctx context.Context, enabled bool) (bool, error)
}

// QuerySimulator reads the demo Simulator of orders, restaurants and delivery
// on behalf of the verified user (FR-607).
func (s *BffService) QuerySimulator(ctx context.Context, _ *service.QuerySimulatorRequest) (*service.QuerySimulatorResponse, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	enabled, err := s.simulator.getSimulator.Handle(ctx)
	if err != nil {
		return nil, simulatorError("simulator", err)
	}
	return &service.QuerySimulatorResponse{Simulator: &service.Simulator{Enabled: enabled}}, nil
}

// MutationSetSimulatorEnabled switches the demo Simulator in all three
// contexts on behalf of the verified user (AC-766).
func (s *BffService) MutationSetSimulatorEnabled(ctx context.Context, request *service.MutationSetSimulatorEnabledRequest) (*service.MutationSetSimulatorEnabledResponse, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	enabled, err := s.simulator.setSimulatorEnabled.Handle(ctx, request.GetEnabled())
	if err != nil {
		return nil, simulatorError("setSimulatorEnabled", err)
	}
	return &service.MutationSetSimulatorEnabledResponse{SetSimulatorEnabled: &service.Simulator{Enabled: enabled}}, nil
}

func simulatorError(operation string, err error) error {
	if errors.Is(err, applications.ErrNotPermitted) {
		return status.Error(codes.PermissionDenied, "simulator operation is not permitted")
	}
	return internalError(operation, err)
}
