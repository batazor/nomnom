package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courierpool/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

type getCourierPoolHandler interface {
	Handle(ctx context.Context) (applications.Pool, error)
}

type changeCourierPoolHandler interface {
	Handle(ctx context.Context, request applications.Request) (applications.Result, error)
}

// courierPool holds the scenarios of the Courier Pool of the flow board.
type courierPool struct {
	getPool    getCourierPoolHandler
	changePool changeCourierPoolHandler
}

// QueryCourierPool shows the Courier Pool to a dispatcher or the operator; any
// other user gets PERMISSION_DENIED, so the screen hides the pool (FR-627).
func (s *BffService) QueryCourierPool(ctx context.Context, _ *service.QueryCourierPoolRequest) (*service.QueryCourierPoolResponse, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	pool, err := s.courierPool.getPool.Handle(ctx)
	if err != nil {
		return nil, courierPoolError("courierPool", err)
	}
	message, err := courierPoolMessage(pool)
	if err != nil {
		return nil, internalError("courierPool", err)
	}
	return &service.QueryCourierPoolResponse{CourierPool: message}, nil
}

// LookupCourierPoolById resolves courierPoolChanged events (ADR 36) with the
// subscriber's access token; the pool has one key, the platform.
func (s *BffService) LookupCourierPoolById(ctx context.Context, request *service.LookupCourierPoolByIdRequest) (*service.LookupCourierPoolByIdResponse, error) {
	pools := make([]*service.CourierPool, 0, len(request.GetKeys()))
	for _, key := range request.GetKeys() {
		if key.GetId() != applications.PoolID {
			return nil, status.Error(codes.NotFound, "courier pool not found")
		}
		response, err := s.QueryCourierPool(ctx, &service.QueryCourierPoolRequest{})
		if err != nil {
			return nil, err
		}
		pools = append(pools, response.GetCourierPool())
	}
	return &service.LookupCourierPoolByIdResponse{Result: pools}, nil
}

func (s *BffService) changeCourierPool(ctx context.Context, operation string, request applications.Request) (*service.CourierPoolPayload, error) {
	if request.Command != applications.CommandAdd && request.CourierID == "" {
		return nil, status.Error(codes.InvalidArgument, "courierId is required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.courierPool.changePool.Handle(ctx, request)
	if err != nil {
		return nil, courierPoolError(operation, err)
	}
	payload, err := courierPoolPayload(result)
	if err != nil {
		return nil, internalError(operation, err)
	}
	return payload, nil
}

// MutationAddCourier registers an available courier at the start point of
// new couriers (FR-623).
func (s *BffService) MutationAddCourier(ctx context.Context, request *service.MutationAddCourierRequest) (*service.MutationAddCourierResponse, error) {
	payload, err := s.changeCourierPool(ctx, "addCourier", applications.Request{
		Command: applications.CommandAdd,
		Name:    request.GetName(),
	})
	if err != nil {
		return nil, err
	}
	return &service.MutationAddCourierResponse{AddCourier: payload}, nil
}

// MutationSetCourierOnLine puts any courier on line or takes them off line.
func (s *BffService) MutationSetCourierOnLine(ctx context.Context, request *service.MutationSetCourierOnLineRequest) (*service.MutationSetCourierOnLineResponse, error) {
	command := applications.CommandTakeOffLine
	if request.GetOnLine() {
		command = applications.CommandPutOnLine
	}
	payload, err := s.changeCourierPool(ctx, "setCourierOnLine", applications.Request{
		Command:   command,
		CourierID: request.GetCourierId(),
	})
	if err != nil {
		return nil, err
	}
	return &service.MutationSetCourierOnLineResponse{SetCourierOnLine: payload}, nil
}

// MutationRetireCourier takes a courier off line out of the Courier Pool.
func (s *BffService) MutationRetireCourier(ctx context.Context, request *service.MutationRetireCourierRequest) (*service.MutationRetireCourierResponse, error) {
	payload, err := s.changeCourierPool(ctx, "retireCourier", applications.Request{
		Command:   applications.CommandRetire,
		CourierID: request.GetCourierId(),
	})
	if err != nil {
		return nil, err
	}
	return &service.MutationRetireCourierResponse{RetireCourier: payload}, nil
}

func courierPoolError(operation string, err error) error {
	switch {
	case errors.Is(err, applications.ErrNotPermitted):
		return status.Error(codes.PermissionDenied, "courier pool is not permitted")
	case errors.Is(err, applications.ErrCourierNotFound):
		return status.Error(codes.NotFound, "courier not found")
	}
	return internalError(operation, err)
}
