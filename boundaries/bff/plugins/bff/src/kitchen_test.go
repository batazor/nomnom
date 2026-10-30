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

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/kitchen/applications"
)

func kitchenService(t *testing.T, get *mockGetKitchenHandler, action *mockKitchenActionHandler) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{accessTokens: verifier, kitchen: &kitchen{getKitchen: get, applyAction: action}}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

var (
	kitchenReceivedAt      = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	kitchenAcceptedAt      = kitchenReceivedAt.Add(time.Minute)
	kitchenExpectedReadyAt = kitchenReceivedAt.Add(16 * time.Minute)
)

var preparing = applications.OrderView{
	ID: "ro-1", OrderID: "order-1", Stage: applications.StagePreparing, Priority: applications.PriorityPremium,
	Items:           []applications.ItemView{{Name: "Фо бо", Quantity: 2}},
	ReceivedAt:      kitchenReceivedAt,
	AcceptedAt:      &kitchenAcceptedAt,
	ExpectedReadyAt: &kitchenExpectedReadyAt,
}

var received = applications.OrderView{
	ID: "ro-2", OrderID: "order-2", Stage: applications.StageReceived, Priority: applications.PriorityStandard,
	Items:      []applications.ItemView{{Name: "Фо бо", Quantity: 1}},
	ReceivedAt: kitchenReceivedAt,
}

func TestKitchenShowsTheBoardOnBehalfOfTheUser(t *testing.T) {
	get := newMockGetKitchenHandler(t)
	// the user reaches restaurants as a user assertion.
	get.EXPECT().Handle(mock.MatchedBy(func(ctx context.Context) bool { user, ok := identity.UserFrom(ctx); return ok && user.ID != "" }), "restaurant-1").Return(applications.KitchenView{
		Restaurant: applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true},
		Orders:     []applications.OrderView{preparing, received},
	}, nil).Once()
	svc, ctx := kitchenService(t, get, newMockKitchenActionHandler(t))

	response, err := svc.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})

	require.NoError(t, err)
	kitchenMessage := response.GetKitchen()
	assert.Equal(t, "Кухня №8", kitchenMessage.GetRestaurant().GetName())
	assert.True(t, kitchenMessage.GetRestaurant().GetAcceptingOrders())
	require.Len(t, kitchenMessage.GetOrders(), 2)
	order := kitchenMessage.GetOrders()[0]
	assert.Equal(t, "ro-1", order.GetId())
	assert.Equal(t, "order-1", order.GetOrderId())
	assert.Equal(t, service.KitchenOrderStage_KITCHEN_ORDER_STAGE_PREPARING, order.GetStage())
	assert.Equal(t, "2026-10-25T12:00:00Z", order.GetReceivedAt())
	assert.Equal(t, "2026-10-25T12:01:00Z", order.GetAcceptedAt().GetValue())
	assert.Equal(t, "2026-10-25T12:16:00Z", order.GetExpectedReadyAt().GetValue())
	assert.Equal(t, "Фо бо", order.GetItems()[0].GetName())
	assert.Equal(t, int32(2), order.GetItems()[0].GetQuantity())
	waiting := kitchenMessage.GetOrders()[1]
	assert.Nil(t, waiting.GetAcceptedAt(), "acceptedAt is null before Acceptance")
	assert.Nil(t, waiting.GetExpectedReadyAt(), "expectedReadyAt is null before Acceptance")
}

func TestKitchenNotFoundIsNull(t *testing.T) {
	get := newMockGetKitchenHandler(t)
	get.EXPECT().Handle(mock.Anything, "restaurant-1").Return(applications.KitchenView{}, applications.ErrKitchenNotFound).Once()
	svc, ctx := kitchenService(t, get, newMockKitchenActionHandler(t))
	response, err := svc.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})
	require.NoError(t, err)
	assert.Nil(t, response.GetKitchen())

	get = newMockGetKitchenHandler(t)
	get.EXPECT().Handle(mock.Anything, "restaurant-1").Return(applications.KitchenView{}, errors.New("restaurants is down")).Once()
	svc, ctx = kitchenService(t, get, newMockKitchenActionHandler(t))
	_, err = svc.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestKitchenRequiresAccessTokenAndRestaurant(t *testing.T) {
	get := newMockGetKitchenHandler(t)
	svc, _ := kitchenService(t, get, newMockKitchenActionHandler(t))
	_, err := svc.QueryKitchen(context.Background(), &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, ctx := kitchenService(t, get, newMockKitchenActionHandler(t))
	_, err = svc.QueryKitchen(ctx, &service.QueryKitchenRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestApplyKitchenActionPayload(t *testing.T) {
	order := preparing
	action := newMockKitchenActionHandler(t)
	action.EXPECT().Handle(mock.Anything, applications.ActionRequest{RestaurantID: "restaurant-1", KitchenOrderID: "ro-1", Action: applications.ActionMarkAssembled}).
		Return(applications.ActionResult{Order: &order}, nil).Once()
	action.EXPECT().Handle(mock.Anything, applications.ActionRequest{RestaurantID: "restaurant-1", KitchenOrderID: "ro-1", Action: applications.ActionAccept}).
		Return(applications.ActionResult{Problem: applications.ProblemOrderStageConflict}, nil).Once()
	svc, ctx := kitchenService(t, newMockGetKitchenHandler(t), action)

	response, err := svc.MutationApplyKitchenAction(ctx, &service.MutationApplyKitchenActionRequest{Input: &service.KitchenActionInput{
		RestaurantId: "restaurant-1", KitchenOrderId: "ro-1", Action: service.KitchenAction_KITCHEN_ACTION_MARK_ASSEMBLED,
	}})

	require.NoError(t, err)
	assert.Equal(t, "ro-1", response.GetApplyKitchenAction().GetOrder().GetId())
	assert.Equal(t, service.OrderPriority_ORDER_PRIORITY_PREMIUM, response.GetApplyKitchenAction().GetOrder().GetPriority())
	assert.Equal(t, service.KitchenActionProblemCode_KITCHEN_ACTION_PROBLEM_CODE_UNSPECIFIED, response.GetApplyKitchenAction().GetProblem(), "no problem is null")

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
		action := newMockKitchenActionHandler(t)
		action.EXPECT().Handle(mock.Anything, applications.ActionRequest{RestaurantID: "restaurant-1", KitchenOrderID: "ro-1", Action: applications.ActionReject}).
			Return(applications.ActionResult{}, refusal).Once()
		svc, ctx := kitchenService(t, newMockGetKitchenHandler(t), action)
		_, err := svc.MutationApplyKitchenAction(ctx, &service.MutationApplyKitchenActionRequest{Input: input})
		assert.Equal(t, code, status.Code(err), refusal.Error())
	}

	svc, ctx := kitchenService(t, newMockGetKitchenHandler(t), newMockKitchenActionHandler(t))
	for _, bad := range []*service.KitchenActionInput{
		{KitchenOrderId: "ro-1", Action: service.KitchenAction_KITCHEN_ACTION_ACCEPT},
		{RestaurantId: "restaurant-1", Action: service.KitchenAction_KITCHEN_ACTION_ACCEPT},
		{RestaurantId: "restaurant-1", KitchenOrderId: "ro-1"},
	} {
		_, err := svc.MutationApplyKitchenAction(ctx, &service.MutationApplyKitchenActionRequest{Input: bad})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

// FR-655: startNextPreparation answers the started order, null on an empty
// queue and NO_COOK_AVAILABLE without a free cook.
func TestStartNextPreparationPayload(t *testing.T) {
	order := preparing
	start := newMockStartNextPreparationHandler(t)
	start.EXPECT().Handle(mock.Anything, applications.StartNextRequest{RestaurantID: "restaurant-1", RequestID: "attempt-1"}).
		Return(applications.ActionResult{Order: &order}, nil).Once()
	start.EXPECT().Handle(mock.Anything, applications.StartNextRequest{RestaurantID: "restaurant-1", RequestID: "attempt-2"}).
		Return(applications.ActionResult{}, nil).Once()
	start.EXPECT().Handle(mock.Anything, applications.StartNextRequest{RestaurantID: "restaurant-1", RequestID: "attempt-3"}).
		Return(applications.ActionResult{Problem: applications.ProblemNoCookAvailable}, nil).Once()
	svc, ctx := kitchenService(t, newMockGetKitchenHandler(t), newMockKitchenActionHandler(t))
	svc.kitchen.startNext = start

	response, err := svc.MutationStartNextPreparation(ctx, &service.MutationStartNextPreparationRequest{RestaurantId: "restaurant-1", RequestId: "attempt-1"})
	require.NoError(t, err)
	assert.Equal(t, "ro-1", response.GetStartNextPreparation().GetOrder().GetId())

	response, err = svc.MutationStartNextPreparation(ctx, &service.MutationStartNextPreparationRequest{RestaurantId: "restaurant-1", RequestId: "attempt-2"})
	require.NoError(t, err)
	assert.Nil(t, response.GetStartNextPreparation().GetOrder(), "an empty queue starts nothing")
	assert.Equal(t, service.KitchenActionProblemCode_KITCHEN_ACTION_PROBLEM_CODE_UNSPECIFIED, response.GetStartNextPreparation().GetProblem())

	response, err = svc.MutationStartNextPreparation(ctx, &service.MutationStartNextPreparationRequest{RestaurantId: "restaurant-1", RequestId: "attempt-3"})
	require.NoError(t, err)
	assert.Equal(t, service.KitchenActionProblemCode_KITCHEN_ACTION_PROBLEM_CODE_NO_COOK_AVAILABLE, response.GetStartNextPreparation().GetProblem())

	_, err = svc.MutationStartNextPreparation(ctx, &service.MutationStartNextPreparationRequest{RestaurantId: "restaurant-1"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err), "requestId is required")
}

// AC-932: the scheduled orders are their own column with the window and the
// planned ready time; an incoming scheduled order carries its window.
func TestKitchenShowsTheScheduledOrders(t *testing.T) {
	window := applications.Window{Start: kitchenReceivedAt.Add(2 * time.Hour), End: kitchenReceivedAt.Add(2*time.Hour + 15*time.Minute)}
	incoming := received
	incoming.DeliveryWindow = &window
	get := newMockGetKitchenHandler(t)
	get.EXPECT().Handle(mock.Anything, "restaurant-1").Return(applications.KitchenView{
		Restaurant: applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true},
		Orders:     []applications.OrderView{incoming},
		Scheduled: []applications.ScheduledOrderView{{
			ID: "ro-scheduled", OrderID: "order-scheduled", Priority: applications.PriorityPremium,
			Items:          []applications.ItemView{{Name: "Фо бо", Quantity: 1}},
			DeliveryWindow: window, PlannedReadyAt: window.Start.Add(-20 * time.Minute),
		}},
	}, nil).Once()
	svc, ctx := kitchenService(t, get, newMockKitchenActionHandler(t))

	response, err := svc.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: "restaurant-1"})

	require.NoError(t, err)
	kitchenMessage := response.GetKitchen()
	assert.Equal(t, &service.OrderDeliveryWindow{Start: "2026-10-25T14:00:00Z", End: "2026-10-25T14:15:00Z"}, kitchenMessage.GetOrders()[0].GetDeliveryWindow())
	require.Len(t, kitchenMessage.GetScheduledOrders(), 1)
	scheduled := kitchenMessage.GetScheduledOrders()[0]
	assert.Equal(t, "ro-scheduled", scheduled.GetId())
	assert.Equal(t, service.OrderPriority_ORDER_PRIORITY_PREMIUM, scheduled.GetPriority())
	assert.Equal(t, "2026-10-25T13:40:00Z", scheduled.GetPlannedReadyAt())
	assert.Equal(t, "2026-10-25T14:00:00Z", scheduled.GetDeliveryWindow().GetStart())
	assert.Equal(t, "Фо бо", scheduled.GetItems()[0].GetName())
}
