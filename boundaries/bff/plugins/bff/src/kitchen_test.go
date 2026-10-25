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

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
)

type fakeGetKitchen struct {
	view    applications.KitchenView
	err     error
	called  bool
	hasUser bool
}

func (f *fakeGetKitchen) Handle(ctx context.Context, _ string) (applications.KitchenView, error) {
	f.called = true
	_, f.hasUser = identity.UserFrom(ctx)
	return f.view, f.err
}

type fakeKitchenAction struct {
	result applications.ActionResult
	err    error
	got    applications.ActionRequest
	called bool
}

func (f *fakeKitchenAction) Handle(_ context.Context, request applications.ActionRequest) (applications.ActionResult, error) {
	f.called, f.got = true, request
	return f.result, f.err
}

func kitchenService(t *testing.T, get *fakeGetKitchen, action *fakeKitchenAction) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{accessTokens: verifier, kitchen: &kitchen{getKitchen: get, applyAction: action}}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

var kitchenReceivedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

var preparing = applications.OrderView{
	ID: "ro-1", OrderID: "order-1", Stage: applications.StagePreparing,
	Items:      []applications.ItemView{{Name: "Фо бо", Quantity: 2}},
	ReceivedAt: kitchenReceivedAt,
}

func TestKitchenShowsTheBoardOnBehalfOfTheUser(t *testing.T) {
	get := &fakeGetKitchen{view: applications.KitchenView{
		Restaurant: applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", Address: "ул. Садовая, 8", IsOpen: true},
		Orders:     []applications.OrderView{preparing},
	}}
	svc, ctx := kitchenService(t, get, &fakeKitchenAction{})

	response, err := svc.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})

	require.NoError(t, err)
	assert.True(t, get.hasUser, "the user reaches restaurants as a user assertion")
	kitchenMessage := response.GetKitchen()
	assert.Equal(t, "Кухня №8", kitchenMessage.GetRestaurant().GetName())
	assert.True(t, kitchenMessage.GetRestaurant().GetAcceptingOrders())
	assert.Nil(t, kitchenMessage.GetRestaurant().GetMenu())
	require.Len(t, kitchenMessage.GetOrders(), 1)
	order := kitchenMessage.GetOrders()[0]
	assert.Equal(t, "ro-1", order.GetId())
	assert.Equal(t, "order-1", order.GetOrderId())
	assert.Equal(t, service.KitchenOrderStage_KITCHEN_ORDER_STAGE_PREPARING, order.GetStage())
	assert.Equal(t, "2026-10-25T12:00:00Z", order.GetReceivedAt())
	assert.Equal(t, "Фо бо", order.GetItems()[0].GetName())
	assert.Equal(t, int32(2), order.GetItems()[0].GetQuantity())
}

func TestKitchenNotFoundIsNull(t *testing.T) {
	svc, ctx := kitchenService(t, &fakeGetKitchen{err: applications.ErrKitchenNotFound}, &fakeKitchenAction{})
	response, err := svc.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})
	require.NoError(t, err)
	assert.Nil(t, response.GetKitchen())

	svc, ctx = kitchenService(t, &fakeGetKitchen{err: errors.New("restaurants is down")}, &fakeKitchenAction{})
	_, err = svc.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestKitchenRequiresAccessTokenAndRestaurant(t *testing.T) {
	get := &fakeGetKitchen{}
	svc, _ := kitchenService(t, get, &fakeKitchenAction{})
	_, err := svc.QueryKitchen(context.Background(), &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, ctx := kitchenService(t, get, &fakeKitchenAction{})
	_, err = svc.QueryKitchen(ctx, &service.QueryKitchenRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.False(t, get.called)

	svc.kitchen = nil
	_, err = svc.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestApplyKitchenActionPayload(t *testing.T) {
	order := preparing
	action := &fakeKitchenAction{result: applications.ActionResult{Order: &order}}
	svc, ctx := kitchenService(t, &fakeGetKitchen{}, action)

	response, err := svc.MutationApplyKitchenAction(ctx, &service.MutationApplyKitchenActionRequest{Input: &service.KitchenActionInput{
		RestaurantId: "restaurant-1", KitchenOrderId: "ro-1", Action: service.KitchenAction_KITCHEN_ACTION_START_PREPARATION,
	}})

	require.NoError(t, err)
	assert.Equal(t, applications.ActionRequest{RestaurantID: "restaurant-1", KitchenOrderID: "ro-1", Action: applications.ActionStartPreparation}, action.got)
	assert.Equal(t, "ro-1", response.GetApplyKitchenAction().GetOrder().GetId())
	assert.Equal(t, service.KitchenActionProblemCode_KITCHEN_ACTION_PROBLEM_CODE_UNSPECIFIED, response.GetApplyKitchenAction().GetProblem(), "no problem is null")

	action.result = applications.ActionResult{Problem: applications.ProblemOrderStageConflict}
	response, err = svc.MutationApplyKitchenAction(ctx, &service.MutationApplyKitchenActionRequest{Input: &service.KitchenActionInput{
		RestaurantId: "restaurant-1", KitchenOrderId: "ro-1", Action: service.KitchenAction_KITCHEN_ACTION_ACCEPT,
	}})
	require.NoError(t, err)
	assert.Nil(t, response.GetApplyKitchenAction().GetOrder())
	assert.Equal(t, service.KitchenActionProblemCode_KITCHEN_ACTION_PROBLEM_CODE_ORDER_STAGE_CONFLICT, response.GetApplyKitchenAction().GetProblem())
}

func TestApplyKitchenActionErrors(t *testing.T) {
	input := &service.KitchenActionInput{RestaurantId: "restaurant-1", KitchenOrderId: "ro-1", Action: service.KitchenAction_KITCHEN_ACTION_REJECT}
	for refusal, code := range map[error]codes.Code{
		applications.ErrOrderNotFound:  codes.NotFound,
		applications.ErrNotPermitted:   codes.PermissionDenied,
		applications.ErrInvalidCommand: codes.InvalidArgument,
		errors.New("down"):             codes.Unavailable,
	} {
		svc, ctx := kitchenService(t, &fakeGetKitchen{}, &fakeKitchenAction{err: refusal})
		_, err := svc.MutationApplyKitchenAction(ctx, &service.MutationApplyKitchenActionRequest{Input: input})
		assert.Equal(t, code, status.Code(err), refusal.Error())
	}

	action := &fakeKitchenAction{}
	svc, ctx := kitchenService(t, &fakeGetKitchen{}, action)
	for _, bad := range []*service.KitchenActionInput{
		{KitchenOrderId: "ro-1", Action: service.KitchenAction_KITCHEN_ACTION_ACCEPT},
		{RestaurantId: "restaurant-1", Action: service.KitchenAction_KITCHEN_ACTION_ACCEPT},
		{RestaurantId: "restaurant-1", KitchenOrderId: "ro-1"},
	} {
		_, err := svc.MutationApplyKitchenAction(ctx, &service.MutationApplyKitchenActionRequest{Input: bad})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}
	assert.False(t, action.called)
}
