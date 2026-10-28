package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courierpool/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

func courierPoolService(t *testing.T, get *mockGetCourierPoolHandler, change *mockChangeCourierPoolHandler) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{
		accessTokens: verifier,
		courierPool:  &courierPool{getPool: get, changePool: change},
	}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

var pool = applications.Pool{Market: "berlin", Size: 2, OnLine: 1, Busy: 1, Waiting: 2, Couriers: []applications.Courier{
	{ID: "courier-1", Name: "Тимур", Availability: applications.AvailabilityBusy, CurrentOrderID: "order-1"},
	{ID: "courier-2", Name: "Анна", Availability: applications.AvailabilityUnavailable},
}}

// ADR 41: the pool is the entity of its Market; the key of another Market is
// not found.
func TestCourierPoolIsTheEntityOfItsMarket(t *testing.T) {
	get := newMockGetCourierPoolHandler(t)
	get.EXPECT().Handle(mock.Anything).Return(pool, nil).Times(3)
	svc, ctx := courierPoolService(t, get, newMockChangeCourierPoolHandler(t))

	response, err := svc.QueryCourierPool(ctx, &service.QueryCourierPoolRequest{})

	require.NoError(t, err)
	message := response.GetCourierPool()
	assert.Equal(t, "berlin", message.GetId())
	assert.Equal(t, []int32{2, 1, 1, 2}, []int32{message.GetSize(), message.GetOnLine(), message.GetBusy(), message.GetWaiting()})
	assert.Equal(t, "order-1", message.GetCouriers()[0].GetCurrentOrderId().GetValue())
	assert.Nil(t, message.GetCouriers()[1].GetCurrentOrderId())

	lookup, err := svc.LookupCourierPoolById(ctx, &service.LookupCourierPoolByIdRequest{Keys: []*service.LookupCourierPoolByIdRequestKey{{Id: "berlin"}}})
	require.NoError(t, err)
	assert.Len(t, lookup.GetResult(), 1)
	_, err = svc.LookupCourierPoolById(ctx, &service.LookupCourierPoolByIdRequest{Keys: []*service.LookupCourierPoolByIdRequestKey{{Id: "hamburg"}}})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// AC-798: a user who may not manage couriers gets PERMISSION_DENIED.
func TestCourierPoolOfAnotherUserIsPermissionDenied(t *testing.T) {
	get := newMockGetCourierPoolHandler(t)
	get.EXPECT().Handle(mock.Anything).Return(applications.Pool{}, applications.ErrNotPermitted).Once()
	svc, ctx := courierPoolService(t, get, newMockChangeCourierPoolHandler(t))

	_, err := svc.QueryCourierPool(ctx, &service.QueryCourierPoolRequest{})

	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestCourierPoolCommandsAnswerThePoolAndTheProblem(t *testing.T) {
	result := applications.Result{Pool: pool, Problem: applications.ProblemCourierStatusConflict}
	change := newMockChangeCourierPoolHandler(t)
	change.EXPECT().Handle(mock.Anything, applications.Request{Command: applications.CommandTakeOffLine, CourierID: "courier-1"}).Return(result, nil).Once()
	change.EXPECT().Handle(mock.Anything, applications.Request{Command: applications.CommandAdd, Name: "Сергей"}).Return(result, nil).Once()
	svc, ctx := courierPoolService(t, newMockGetCourierPoolHandler(t), change)

	response, err := svc.MutationSetCourierOnLine(ctx, &service.MutationSetCourierOnLineRequest{CourierId: "courier-1", OnLine: false})

	require.NoError(t, err)
	assert.Equal(t, service.CourierPoolProblemCode_COURIER_POOL_PROBLEM_CODE_COURIER_STATUS_CONFLICT, response.GetSetCourierOnLine().GetProblem())

	_, err = svc.MutationAddCourier(ctx, &service.MutationAddCourierRequest{Name: "Сергей"})
	require.NoError(t, err)

	_, err = svc.MutationRetireCourier(ctx, &service.MutationRetireCourierRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestOfferDeliveryToCourierAnswersThePoolAndTheProblem(t *testing.T) {
	change := newMockChangeCourierPoolHandler(t)
	change.EXPECT().Handle(mock.Anything, applications.Request{Command: applications.CommandOffer, CourierID: "courier-2", OrderID: "order-1"}).
		Return(applications.Result{Pool: pool, Problem: applications.ProblemOfferRefused}, nil).Once()
	svc, ctx := courierPoolService(t, newMockGetCourierPoolHandler(t), change)

	response, err := svc.MutationOfferDeliveryToCourier(ctx, &service.MutationOfferDeliveryToCourierRequest{OrderId: "order-1", CourierId: "courier-2"})

	require.NoError(t, err)
	assert.Equal(t, service.CourierPoolProblemCode_COURIER_POOL_PROBLEM_CODE_OFFER_REFUSED, response.GetOfferDeliveryToCourier().GetProblem())
	assert.Equal(t, "berlin", response.GetOfferDeliveryToCourier().GetCourierPool().GetId())

	_, err = svc.MutationOfferDeliveryToCourier(ctx, &service.MutationOfferDeliveryToCourierRequest{CourierId: "courier-2"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
