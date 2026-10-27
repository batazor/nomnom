package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

const fixtures = "../../../../../tests/identity/fixtures"

// onBehalfOf matches a context that carries the verified user userID.
func onBehalfOf(userID string) any {
	return mock.MatchedBy(func(ctx context.Context) bool {
		user, ok := identity.UserFrom(ctx)
		return ok && user.ID == userID
	})
}

// withUser matches a context that carries a verified user.
func withUser() any {
	return mock.MatchedBy(func(ctx context.Context) bool {
		user, ok := identity.UserFrom(ctx)
		return ok && user.ID != ""
	})
}

func accessToken(t *testing.T) (verifier *identity.AccessTokenVerifier, token string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, "idp-vectors.json"))
	require.NoError(t, err)
	var vectors struct {
		Issuer   string            `json:"issuer"`
		Audience string            `json:"audience"`
		Tokens   map[string]string `json:"tokens"`
	}
	require.NoError(t, json.Unmarshal(data, &vectors))
	verifier, err = identity.NewAccessTokenVerifier(context.Background(), vectors.Audience,
		[]identity.Provider{{Issuer: vectors.Issuer, JWKSFile: filepath.Join(fixtures, "idp-jwks.json")}})
	require.NoError(t, err)
	return verifier, vectors.Tokens["valid"]
}

func newService(t *testing.T, getOrder *mockGetOrderHandler, cancel *mockRequestCancellationHandler) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{
		accessTokens: verifier,
		tracking:     &orderTracking{getOrder: getOrder, requestCancellation: cancel},
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	return svc, ctx
}

// AC-556: without an access token nothing reaches orders.
func TestQueryOrderRequiresAccessToken(t *testing.T) {
	svc, _ := newService(t, newMockGetOrderHandler(t), newMockRequestCancellationHandler(t))

	_, err := svc.QueryOrder(context.Background(), &service.QueryOrderRequest{Id: "order-1"})

	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestQueryOrderMapsTheView(t *testing.T) {
	placed := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	until := placed.Add(5 * time.Minute)
	ready := placed.Add(12 * time.Minute)
	getOrder := newMockGetOrderHandler(t)
	// orders is called on behalf of the customer.
	getOrder.EXPECT().Handle(onBehalfOf("customer-1"), "order-1").Return(applications.OrderView{
		ID: "order-1", Status: applications.StatusPreparing,
		Restaurant: applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true},
		Items: []applications.ItemView{{MenuItemID: "menu-tea", Name: "Чай", Quantity: 3,
			UnitPrice: applications.Money{Amount: "0.10", Currency: "EUR"}, LineTotal: applications.Money{Amount: "0.30", Currency: "EUR"}}},
		Total:          applications.Money{Amount: "0.30", Currency: "EUR"},
		PlacedAt:       placed,
		Milestones:     []applications.Milestone{{Kind: applications.MilestoneCourierAssigned, OccurredAt: placed.Add(time.Minute)}},
		AvailableUntil: &until,
		Deadlines:      applications.Deadlines{ExpectedReadyAt: &ready},
	}, nil).Once()
	svc, ctx := newService(t, getOrder, newMockRequestCancellationHandler(t))

	response, err := svc.QueryOrder(ctx, &service.QueryOrderRequest{Id: "order-1"})

	require.NoError(t, err)
	order := response.GetOrder()
	assert.Equal(t, service.OrderStatus_ORDER_STATUS_PREPARING, order.GetStatus())
	assert.Equal(t, service.OrderEndReason_ORDER_END_REASON_UNSPECIFIED, order.GetEndReason())
	assert.Equal(t, "0.30", order.GetItems()[0].GetLineTotal().GetAmount())
	assert.Equal(t, service.OrderMilestoneKind_ORDER_MILESTONE_KIND_COURIER_ASSIGNED, order.GetMilestones()[0].GetKind())
	assert.Equal(t, "2026-10-25T12:00:00Z", order.GetPlacedAt())
	assert.Equal(t, "2026-10-25T12:05:00Z", order.GetCancellation().GetAvailableUntil())
	assert.Equal(t, "2026-10-25T12:12:00Z", order.GetExpectedReadyAt().GetValue())
	assert.Nil(t, order.GetExpectedPickupAt(), "a deadline nobody named is null")
	assert.Nil(t, order.GetExpectedDeliveredAt(), "a deadline nobody named is null")
}

func TestQueryOrderNotFoundIsNull(t *testing.T) {
	getOrder := newMockGetOrderHandler(t)
	getOrder.EXPECT().Handle(mock.Anything, "order-1").Return(applications.OrderView{}, applications.ErrOrderNotFound).Once()
	svc, ctx := newService(t, getOrder, newMockRequestCancellationHandler(t))
	response, err := svc.QueryOrder(ctx, &service.QueryOrderRequest{Id: "order-1"})
	require.NoError(t, err)
	assert.Nil(t, response.GetOrder())
}

// ADR 36: an orderChanged event is resolved as the customer, a foreign or
// missing order is NOT_FOUND, never order data.
func TestLookupOrderByIdResolvesAsTheCustomer(t *testing.T) {
	delivered := time.Date(2026, 10, 25, 12, 30, 0, 0, time.UTC)
	getOrder := newMockGetOrderHandler(t)
	getOrder.EXPECT().Handle(onBehalfOf("customer-1"), "order-1").Return(applications.OrderView{ID: "order-1", Status: applications.StatusPickedUp,
		Total:     applications.Money{Amount: "0.30", Currency: "EUR"},
		Deadlines: applications.Deadlines{ExpectedDeliveredAt: &delivered}}, nil).Once()
	getOrder.EXPECT().Handle(onBehalfOf("customer-1"), "order-1").Return(applications.OrderView{}, applications.ErrOrderNotFound).Once()
	svc, ctx := newService(t, getOrder, newMockRequestCancellationHandler(t))
	request := &service.LookupOrderByIdRequest{Keys: []*service.LookupOrderByIdRequestKey{{Id: "order-1"}}}

	response, err := svc.LookupOrderById(ctx, request)
	require.NoError(t, err)
	require.Len(t, response.GetResult(), 1)
	assert.Equal(t, "order-1", response.GetResult()[0].GetId())
	assert.Equal(t, "2026-10-25T12:30:00Z", response.GetResult()[0].GetExpectedDeliveredAt().GetValue(),
		"orderChanged carries the deadlines (FR-611)")

	_, err = svc.LookupOrderById(ctx, request)
	assert.Equal(t, codes.NotFound, status.Code(err))

	_, err = svc.LookupOrderById(context.Background(), request)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestRequestOrderCancellationMapsOutcomeAndProblem(t *testing.T) {
	want := applications.CancellationRequest{OrderID: "order-1", RequestID: "r-1", Reason: "changed mind"}
	cancel := newMockRequestCancellationHandler(t)
	cancel.EXPECT().Handle(mock.Anything, want).Return(applications.CancellationResult{Problem: applications.ProblemWindowExpired}, nil).Once()
	cancel.EXPECT().Handle(mock.Anything, want).Return(applications.CancellationResult{Outcome: applications.OutcomeRequested}, nil).Once()
	cancel.EXPECT().Handle(mock.Anything, want).Return(applications.CancellationResult{}, applications.ErrOrderNotFound).Once()
	svc, ctx := newService(t, newMockGetOrderHandler(t), cancel)
	request := &service.MutationRequestOrderCancellationRequest{Input: &service.RequestOrderCancellationInput{
		OrderId: "order-1", RequestId: "r-1", Reason: wrapperspb.String("changed mind"),
	}}

	response, err := svc.MutationRequestOrderCancellation(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, service.OrderCancellationProblemCode_ORDER_CANCELLATION_PROBLEM_CODE_CANCELLATION_WINDOW_EXPIRED,
		response.GetRequestOrderCancellation().GetProblem().GetCode())

	response, err = svc.MutationRequestOrderCancellation(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, service.OrderCancellationOutcome_ORDER_CANCELLATION_OUTCOME_REQUESTED, response.GetRequestOrderCancellation().GetOutcome())
	assert.Nil(t, response.GetRequestOrderCancellation().GetProblem())

	_, err = svc.MutationRequestOrderCancellation(ctx, request)
	assert.Equal(t, codes.NotFound, status.Code(err))

	_, err = svc.MutationRequestOrderCancellation(ctx, &service.MutationRequestOrderCancellationRequest{Input: &service.RequestOrderCancellationInput{OrderId: "order-1"}})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// AC-641: a status the presenter does not know is an error, not UNSPECIFIED.
func TestQueryOrderWithUnknownStatusIsAnError(t *testing.T) {
	getOrder := newMockGetOrderHandler(t)
	getOrder.EXPECT().Handle(mock.Anything, "order-1").Return(applications.OrderView{ID: "order-1", Status: "LOST"}, nil).Once()
	svc, ctx := newService(t, getOrder, newMockRequestCancellationHandler(t))
	response, err := svc.QueryOrder(ctx, &service.QueryOrderRequest{Id: "order-1"})
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Nil(t, response)
}
