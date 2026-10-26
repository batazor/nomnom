package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courierpool/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

type fakeGetCourierPool struct {
	pool applications.Pool
	err  error
}

func (f *fakeGetCourierPool) Handle(context.Context) (applications.Pool, error) { return f.pool, f.err }

type fakeChangeCourierPool struct {
	result  applications.Result
	err     error
	request applications.Request
}

func (f *fakeChangeCourierPool) Handle(_ context.Context, request applications.Request) (applications.Result, error) {
	f.request = request
	return f.result, f.err
}

func courierPoolService(t *testing.T, get *fakeGetCourierPool, change *fakeChangeCourierPool) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{
		accessTokens: verifier,
		courierPool:  &courierPool{getPool: get, changePool: change},
	}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

var pool = applications.Pool{Size: 2, OnLine: 1, Busy: 1, Waiting: 2, Couriers: []applications.Courier{
	{ID: "courier-1", Name: "Тимур", Availability: applications.AvailabilityBusy, CurrentOrderID: "order-1"},
	{ID: "courier-2", Name: "Анна", Availability: applications.AvailabilityUnavailable},
}}

func TestCourierPoolIsTheEntityOfThePlatform(t *testing.T) {
	svc, ctx := courierPoolService(t, &fakeGetCourierPool{pool: pool}, &fakeChangeCourierPool{})

	response, err := svc.QueryCourierPool(ctx, &service.QueryCourierPoolRequest{})

	require.NoError(t, err)
	message := response.GetCourierPool()
	assert.Equal(t, "nomnom", message.GetId())
	assert.Equal(t, []int32{2, 1, 1, 2}, []int32{message.GetSize(), message.GetOnLine(), message.GetBusy(), message.GetWaiting()})
	assert.Equal(t, "order-1", message.GetCouriers()[0].GetCurrentOrderId().GetValue())
	assert.Nil(t, message.GetCouriers()[1].GetCurrentOrderId())

	lookup, err := svc.LookupCourierPoolById(ctx, &service.LookupCourierPoolByIdRequest{Keys: []*service.LookupCourierPoolByIdRequestKey{{Id: "nomnom"}}})
	require.NoError(t, err)
	assert.Len(t, lookup.GetResult(), 1)
	_, err = svc.LookupCourierPoolById(ctx, &service.LookupCourierPoolByIdRequest{Keys: []*service.LookupCourierPoolByIdRequestKey{{Id: "other"}}})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// AC-798: a user who may not manage couriers gets PERMISSION_DENIED.
func TestCourierPoolOfAnotherUserIsPermissionDenied(t *testing.T) {
	svc, ctx := courierPoolService(t, &fakeGetCourierPool{err: applications.ErrNotPermitted}, &fakeChangeCourierPool{})

	_, err := svc.QueryCourierPool(ctx, &service.QueryCourierPoolRequest{})

	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestCourierPoolCommandsAnswerThePoolAndTheProblem(t *testing.T) {
	change := &fakeChangeCourierPool{result: applications.Result{Pool: pool, Problem: applications.ProblemCourierStatusConflict}}
	svc, ctx := courierPoolService(t, &fakeGetCourierPool{}, change)

	response, err := svc.MutationSetCourierOnLine(ctx, &service.MutationSetCourierOnLineRequest{CourierId: "courier-1", OnLine: false})

	require.NoError(t, err)
	assert.Equal(t, applications.Request{Command: applications.CommandTakeOffLine, CourierID: "courier-1"}, change.request)
	assert.Equal(t, service.CourierPoolProblemCode_COURIER_POOL_PROBLEM_CODE_COURIER_STATUS_CONFLICT, response.GetSetCourierOnLine().GetProblem())

	_, err = svc.MutationAddCourier(ctx, &service.MutationAddCourierRequest{Name: "Сергей"})
	require.NoError(t, err)
	assert.Equal(t, applications.Request{Command: applications.CommandAdd, Name: "Сергей"}, change.request)

	_, err = svc.MutationRetireCourier(ctx, &service.MutationRetireCourierRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
