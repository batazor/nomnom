package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

type fakeGetOrder struct {
	view   applications.OrderView
	err    error
	called bool
	user   identity.User
}

func (f *fakeGetOrder) Handle(ctx context.Context, _ string) (applications.OrderView, error) {
	f.called = true
	f.user, _ = identity.UserFrom(ctx)
	return f.view, f.err
}

type fakeCancellation struct {
	result applications.CancellationResult
	err    error
	got    applications.CancellationRequest
}

func (f *fakeCancellation) Handle(_ context.Context, request applications.CancellationRequest) (applications.CancellationResult, error) {
	f.got = request
	return f.result, f.err
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
	verifier, err = identity.NewAccessTokenVerifier(vectors.Issuer, vectors.Audience, filepath.Join(fixtures, "idp-jwks.json"))
	require.NoError(t, err)
	return verifier, vectors.Tokens["valid"]
}

func newService(t *testing.T, getOrder *fakeGetOrder, cancel *fakeCancellation) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{
		identity: &identity.Identity{AccessTokens: verifier},
		tracking: &orderTracking{getOrder: getOrder, requestCancellation: cancel},
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	return svc, ctx
}

// AC-556: without an access token nothing reaches orders.
func TestQueryOrderRequiresAccessToken(t *testing.T) {
	getOrder := &fakeGetOrder{}
	svc, _ := newService(t, getOrder, &fakeCancellation{})

	_, err := svc.QueryOrder(context.Background(), &service.QueryOrderRequest{Id: "order-1"})

	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.False(t, getOrder.called)
}

func TestQueryOrderMapsTheView(t *testing.T) {
	placed := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	until := placed.Add(5 * time.Minute)
	getOrder := &fakeGetOrder{view: applications.OrderView{
		ID: "order-1", Status: applications.StatusPreparing,
		Restaurant: applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true},
		Items: []applications.ItemView{{MenuItemID: "menu-tea", Name: "Чай", Quantity: 3,
			UnitPrice: applications.Money{Amount: "0.10", Currency: "EUR"}, LineTotal: applications.Money{Amount: "0.30", Currency: "EUR"}}},
		Total:          applications.Money{Amount: "0.30", Currency: "EUR"},
		PlacedAt:       placed,
		Milestones:     []applications.Milestone{{Kind: applications.MilestoneCourierAssigned, OccurredAt: placed.Add(time.Minute)}},
		AvailableUntil: &until,
	}}
	svc, ctx := newService(t, getOrder, &fakeCancellation{})

	response, err := svc.QueryOrder(ctx, &service.QueryOrderRequest{Id: "order-1"})

	require.NoError(t, err)
	order := response.GetOrder()
	assert.Equal(t, "customer-1", getOrder.user.ID, "orders is called on behalf of the customer")
	assert.Equal(t, service.OrderStatus_ORDER_STATUS_PREPARING, order.GetStatus())
	assert.Equal(t, service.OrderEndReason_ORDER_END_REASON_UNSPECIFIED, order.GetEndReason())
	assert.Equal(t, "0.30", order.GetItems()[0].GetLineTotal().GetAmount())
	assert.Equal(t, service.OrderMilestoneKind_ORDER_MILESTONE_KIND_COURIER_ASSIGNED, order.GetMilestones()[0].GetKind())
	assert.Equal(t, "2026-10-25T12:00:00Z", order.GetPlacedAt())
	assert.Equal(t, "2026-10-25T12:05:00Z", order.GetCancellation().GetAvailableUntil())
}

func TestQueryOrderNotFoundIsNull(t *testing.T) {
	svc, ctx := newService(t, &fakeGetOrder{err: applications.ErrOrderNotFound}, &fakeCancellation{})
	response, err := svc.QueryOrder(ctx, &service.QueryOrderRequest{Id: "order-1"})
	require.NoError(t, err)
	assert.Nil(t, response.GetOrder())
}

func TestRequestOrderCancellationMapsOutcomeAndProblem(t *testing.T) {
	cancel := &fakeCancellation{result: applications.CancellationResult{Problem: applications.ProblemWindowExpired}}
	svc, ctx := newService(t, &fakeGetOrder{}, cancel)
	request := &service.MutationRequestOrderCancellationRequest{Input: &service.RequestOrderCancellationInput{
		OrderId: "order-1", RequestId: "r-1", Reason: wrapperspb.String("changed mind"),
	}}

	response, err := svc.MutationRequestOrderCancellation(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, service.OrderCancellationProblemCode_ORDER_CANCELLATION_PROBLEM_CODE_CANCELLATION_WINDOW_EXPIRED,
		response.GetRequestOrderCancellation().GetProblem().GetCode())
	assert.Equal(t, applications.CancellationRequest{OrderID: "order-1", RequestID: "r-1", Reason: "changed mind"}, cancel.got)

	cancel.result = applications.CancellationResult{Outcome: applications.OutcomeRequested}
	response, err = svc.MutationRequestOrderCancellation(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, service.OrderCancellationOutcome_ORDER_CANCELLATION_OUTCOME_REQUESTED, response.GetRequestOrderCancellation().GetOutcome())
	assert.Nil(t, response.GetRequestOrderCancellation().GetProblem())

	cancel.err = applications.ErrOrderNotFound
	_, err = svc.MutationRequestOrderCancellation(ctx, request)
	assert.Equal(t, codes.NotFound, status.Code(err))

	_, err = svc.MutationRequestOrderCancellation(ctx, &service.MutationRequestOrderCancellationRequest{Input: &service.RequestOrderCancellationInput{OrderId: "order-1"}})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCustomerResolversWithoutGatewayAreUnavailable(t *testing.T) {
	verifier, token := accessToken(t)
	svc := &BffService{identity: &identity.Identity{AccessTokens: verifier}}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))

	_, err := svc.QueryOrder(ctx, &service.QueryOrderRequest{Id: "order-1"})
	assert.Equal(t, codes.Unavailable, status.Code(err))

	tracking, err := newOrderTracking(nil, func(string) string { return "gateway:8080" })
	require.NoError(t, err)
	assert.Nil(t, tracking, "no identity")
}
