package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
)

var nightBakery = applications.Restaurant{ID: "restaurant-night-bakery", Name: "Ночная пекарня"}

func managementService(t *testing.T, open, closer *mockOpeningHandler) (*BffService, context.Context) {
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
	open := newMockOpeningHandler(t)
	// the user reaches restaurants as a user assertion.
	open.EXPECT().Handle(mock.MatchedBy(func(ctx context.Context) bool { user, ok := identity.UserFrom(ctx); return ok && user.ID != "" }), nightBakery.ID).Return(applications.OpeningResult{Restaurant: opened, Changed: true}, nil).Once()
	svc, ctx := managementService(t, open, newMockOpeningHandler(t))

	response, err := svc.MutationOpenRestaurant(ctx, &service.MutationOpenRestaurantRequest{RestaurantId: nightBakery.ID})

	require.NoError(t, err)
	payload := response.GetOpenRestaurant()
	assert.True(t, payload.GetChanged())
	assert.Equal(t, nightBakery.ID, payload.GetRestaurant().GetId())
	assert.Equal(t, "Ночная пекарня", payload.GetRestaurant().GetName())
	assert.True(t, payload.GetRestaurant().GetAcceptingOrders())
	assert.Nil(t, payload.GetRestaurant().GetMenu())
}

// AC-752: a repeat is not an error.
func TestCloseRestaurantRepeatIsNotAChange(t *testing.T) {
	closer := newMockOpeningHandler(t)
	closer.EXPECT().Handle(mock.Anything, nightBakery.ID).Return(applications.OpeningResult{Restaurant: nightBakery, Changed: false}, nil).Once()
	svc, ctx := managementService(t, newMockOpeningHandler(t), closer)

	response, err := svc.MutationCloseRestaurant(ctx, &service.MutationCloseRestaurantRequest{RestaurantId: nightBakery.ID})

	require.NoError(t, err)
	assert.False(t, response.GetCloseRestaurant().GetChanged())
	assert.False(t, response.GetCloseRestaurant().GetRestaurant().GetAcceptingOrders())
}

// AC-753: without an access token nothing reaches restaurants.
func TestOpeningRequiresAccessToken(t *testing.T) {
	svc, _ := managementService(t, newMockOpeningHandler(t), newMockOpeningHandler(t))

	_, err := svc.MutationOpenRestaurant(context.Background(), &service.MutationOpenRestaurantRequest{RestaurantId: nightBakery.ID})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = svc.MutationCloseRestaurant(context.Background(), &service.MutationCloseRestaurantRequest{RestaurantId: nightBakery.ID})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestOpeningRequiresARestaurantID(t *testing.T) {
	svc, ctx := managementService(t, newMockOpeningHandler(t), newMockOpeningHandler(t))

	_, err := svc.MutationOpenRestaurant(ctx, &service.MutationOpenRestaurantRequest{})

	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// AC-754: refusals of restaurants become GraphQL errors without internal details.
func TestOpeningMapsRefusalsToErrors(t *testing.T) {
	for refusal, want := range map[error]codes.Code{
		applications.ErrRestaurantNotFound:                     codes.NotFound,
		applications.ErrNotPermitted:                           codes.PermissionDenied,
		applications.ErrInvalidRestaurant:                      codes.InvalidArgument,
		errors.New("open restaurant: rpc error: spicedb down"): codes.Unavailable,
	} {
		open, closer := newMockOpeningHandler(t), newMockOpeningHandler(t)
		open.EXPECT().Handle(mock.Anything, nightBakery.ID).Return(applications.OpeningResult{}, refusal).Once()
		closer.EXPECT().Handle(mock.Anything, nightBakery.ID).Return(applications.OpeningResult{}, refusal).Once()
		svc, ctx := managementService(t, open, closer)

		_, err := svc.MutationOpenRestaurant(ctx, &service.MutationOpenRestaurantRequest{RestaurantId: nightBakery.ID})
		assert.Equal(t, want, status.Code(err), refusal)
		assert.NotContains(t, status.Convert(err).Message(), "spicedb")

		_, err = svc.MutationCloseRestaurant(ctx, &service.MutationCloseRestaurantRequest{RestaurantId: nightBakery.ID})
		assert.Equal(t, want, status.Code(err), refusal)
	}
}

func TestMyRestaurantsListsRestaurantsOfTheUser(t *testing.T) {
	owned := newMockOwnedRestaurantsHandler(t)
	// the user reaches restaurants as a user assertion.
	owned.EXPECT().Handle(mock.MatchedBy(func(ctx context.Context) bool { user, ok := identity.UserFrom(ctx); return ok && user.ID != "" }), 10, "restaurant-0").Return(applications.RestaurantPage{
		Edges:       []applications.RestaurantEdge{{Cursor: nightBakery.ID, Restaurant: nightBakery}},
		HasNextPage: true,
		EndCursor:   nightBakery.ID,
	}, nil).Once()
	svc, ctx := managementService(t, newMockOpeningHandler(t), newMockOpeningHandler(t))
	svc.management.listOwnedRestaurants = owned

	response, err := svc.QueryMyRestaurants(ctx, &service.QueryMyRestaurantsRequest{First: 10, After: wrapperspb.String("restaurant-0")})

	require.NoError(t, err)
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
	svc, _ := managementService(t, newMockOpeningHandler(t), newMockOpeningHandler(t))
	svc.management.listOwnedRestaurants = newMockOwnedRestaurantsHandler(t)

	_, err := svc.QueryMyRestaurants(context.Background(), &service.QueryMyRestaurantsRequest{First: 10})

	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestMyRestaurantsMapsErrors(t *testing.T) {
	svc, ctx := managementService(t, newMockOpeningHandler(t), newMockOpeningHandler(t))

	owned := newMockOwnedRestaurantsHandler(t)
	owned.EXPECT().Handle(mock.Anything, 101, "").Return(applications.RestaurantPage{}, applications.ErrInvalidPageSize).Once()
	svc.management.listOwnedRestaurants = owned
	_, err := svc.QueryMyRestaurants(ctx, &service.QueryMyRestaurantsRequest{First: 101})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	owned = newMockOwnedRestaurantsHandler(t)
	owned.EXPECT().Handle(mock.Anything, 10, "").Return(applications.RestaurantPage{}, errors.New("restaurants is down")).Once()
	svc.management.listOwnedRestaurants = owned
	_, err = svc.QueryMyRestaurants(ctx, &service.QueryMyRestaurantsRequest{First: 10})
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.NotContains(t, err.Error(), "restaurants is down")
}
