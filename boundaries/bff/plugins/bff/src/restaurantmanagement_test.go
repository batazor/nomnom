package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
)

type fakeOpening struct {
	result       applications.OpeningResult
	err          error
	called       bool
	restaurantID string
	user         identity.User
	hasUser      bool
}

func (f *fakeOpening) Handle(ctx context.Context, restaurantID string) (applications.OpeningResult, error) {
	f.called, f.restaurantID = true, restaurantID
	f.user, f.hasUser = identity.UserFrom(ctx)
	return f.result, f.err
}

var nightBakery = applications.Restaurant{ID: "restaurant-night-bakery", Name: "Ночная пекарня"}

func managementService(t *testing.T, open, closer *fakeOpening) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{
		accessTokens: verifier,
		management:   &restaurantManagement{openRestaurant: open, closeRestaurant: closer},
	}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

// AC-751: the owner opens the restaurant on their own behalf.
func TestOpenRestaurantRunsOnBehalfOfTheUser(t *testing.T) {
	opened := nightBakery
	opened.IsOpen = true
	open := &fakeOpening{result: applications.OpeningResult{Restaurant: opened, Changed: true}}
	svc, ctx := managementService(t, open, &fakeOpening{})

	response, err := svc.MutationOpenRestaurant(ctx, &service.MutationOpenRestaurantRequest{RestaurantId: nightBakery.ID})

	require.NoError(t, err)
	assert.Equal(t, nightBakery.ID, open.restaurantID)
	require.True(t, open.hasUser, "the user reaches restaurants as a user assertion")
	assert.NotEmpty(t, open.user.ID)
	payload := response.GetOpenRestaurant()
	assert.True(t, payload.GetChanged())
	assert.Equal(t, nightBakery.ID, payload.GetRestaurant().GetId())
	assert.Equal(t, "Ночная пекарня", payload.GetRestaurant().GetName())
	assert.True(t, payload.GetRestaurant().GetAcceptingOrders())
	assert.Nil(t, payload.GetRestaurant().GetMenu())
}

// AC-752: a repeat is not an error.
func TestCloseRestaurantRepeatIsNotAChange(t *testing.T) {
	closer := &fakeOpening{result: applications.OpeningResult{Restaurant: nightBakery, Changed: false}}
	svc, ctx := managementService(t, &fakeOpening{}, closer)

	response, err := svc.MutationCloseRestaurant(ctx, &service.MutationCloseRestaurantRequest{RestaurantId: nightBakery.ID})

	require.NoError(t, err)
	assert.True(t, closer.called)
	assert.False(t, response.GetCloseRestaurant().GetChanged())
	assert.False(t, response.GetCloseRestaurant().GetRestaurant().GetAcceptingOrders())
}

// AC-753: without an access token nothing reaches restaurants.
func TestOpeningRequiresAccessToken(t *testing.T) {
	open, closer := &fakeOpening{}, &fakeOpening{}
	svc, _ := managementService(t, open, closer)

	_, err := svc.MutationOpenRestaurant(context.Background(), &service.MutationOpenRestaurantRequest{RestaurantId: nightBakery.ID})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = svc.MutationCloseRestaurant(context.Background(), &service.MutationCloseRestaurantRequest{RestaurantId: nightBakery.ID})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.False(t, open.called)
	assert.False(t, closer.called)
}

func TestOpeningRequiresARestaurantID(t *testing.T) {
	open := &fakeOpening{}
	svc, ctx := managementService(t, open, &fakeOpening{})

	_, err := svc.MutationOpenRestaurant(ctx, &service.MutationOpenRestaurantRequest{})

	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.False(t, open.called)
}

// AC-754: refusals of restaurants become GraphQL errors without internal details.
func TestOpeningMapsRefusalsToErrors(t *testing.T) {
	for refusal, want := range map[error]codes.Code{
		applications.ErrRestaurantNotFound:                     codes.NotFound,
		applications.ErrNotPermitted:                           codes.PermissionDenied,
		applications.ErrInvalidRestaurant:                      codes.InvalidArgument,
		errors.New("open restaurant: rpc error: spicedb down"): codes.Unavailable,
	} {
		svc, ctx := managementService(t, &fakeOpening{err: refusal}, &fakeOpening{err: refusal})

		_, err := svc.MutationOpenRestaurant(ctx, &service.MutationOpenRestaurantRequest{RestaurantId: nightBakery.ID})
		assert.Equal(t, want, status.Code(err), refusal)
		assert.NotContains(t, status.Convert(err).Message(), "spicedb")

		_, err = svc.MutationCloseRestaurant(ctx, &service.MutationCloseRestaurantRequest{RestaurantId: nightBakery.ID})
		assert.Equal(t, want, status.Code(err), refusal)
	}
}

type fakeOwned struct {
	page    applications.RestaurantPage
	err     error
	first   int
	after   string
	hasUser bool
}

func (f *fakeOwned) Handle(ctx context.Context, first int, after string) (applications.RestaurantPage, error) {
	f.first, f.after = first, after
	_, f.hasUser = identity.UserFrom(ctx)
	return f.page, f.err
}

func TestMyRestaurantsListsRestaurantsOfTheUser(t *testing.T) {
	owned := &fakeOwned{page: applications.RestaurantPage{
		Edges:       []applications.RestaurantEdge{{Cursor: nightBakery.ID, Restaurant: nightBakery}},
		HasNextPage: true,
		EndCursor:   nightBakery.ID,
	}}
	svc, ctx := managementService(t, &fakeOpening{}, &fakeOpening{})
	svc.management.listOwnedRestaurants = owned

	response, err := svc.QueryMyRestaurants(ctx, &service.QueryMyRestaurantsRequest{First: 10, After: wrapperspb.String("restaurant-0")})

	require.NoError(t, err)
	assert.Equal(t, 10, owned.first)
	assert.Equal(t, "restaurant-0", owned.after)
	assert.True(t, owned.hasUser, "the user reaches restaurants as a user assertion")
	connection := response.GetMyRestaurants()
	require.Len(t, connection.GetEdges(), 1)
	node := connection.GetEdges()[0].GetNode()
	assert.Equal(t, nightBakery.ID, node.GetId())
	assert.False(t, node.GetAcceptingOrders())
	assert.Nil(t, node.GetMenu())
	assert.True(t, connection.GetPageInfo().GetHasNextPage())
	assert.Equal(t, nightBakery.ID, connection.GetPageInfo().GetEndCursor().GetValue())
}

func TestMyRestaurantsRequiresAccessToken(t *testing.T) {
	owned := &fakeOwned{}
	svc, _ := managementService(t, &fakeOpening{}, &fakeOpening{})
	svc.management.listOwnedRestaurants = owned

	_, err := svc.QueryMyRestaurants(context.Background(), &service.QueryMyRestaurantsRequest{First: 10})

	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Zero(t, owned.first)
}

func TestMyRestaurantsMapsErrors(t *testing.T) {
	svc, ctx := managementService(t, &fakeOpening{}, &fakeOpening{})

	svc.management.listOwnedRestaurants = &fakeOwned{err: applications.ErrInvalidPageSize}
	_, err := svc.QueryMyRestaurants(ctx, &service.QueryMyRestaurantsRequest{First: 101})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	svc.management.listOwnedRestaurants = &fakeOwned{err: errors.New("restaurants is down")}
	_, err = svc.QueryMyRestaurants(ctx, &service.QueryMyRestaurantsRequest{First: 10})
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.NotContains(t, err.Error(), "restaurants is down")
}
