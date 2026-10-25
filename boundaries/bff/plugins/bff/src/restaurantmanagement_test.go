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

var nightBakery = applications.Restaurant{ID: "restaurant-night-bakery", Name: "Ночная пекарня", Address: "ул. Лесная, 12"}

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
	assert.Equal(t, "ул. Лесная, 12", payload.GetRestaurant().GetAddress())
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

	_, err = (&BffService{}).MutationOpenRestaurant(context.Background(), &service.MutationOpenRestaurantRequest{RestaurantId: nightBakery.ID})
	assert.Equal(t, codes.Unauthenticated, status.Code(err), "authentication is not configured")
}

func TestOpeningRequiresARestaurantID(t *testing.T) {
	open := &fakeOpening{}
	svc, ctx := managementService(t, open, &fakeOpening{})

	_, err := svc.MutationOpenRestaurant(ctx, &service.MutationOpenRestaurantRequest{})

	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.False(t, open.called)
}

func TestOpeningWithoutTheSliceIsUnavailable(t *testing.T) {
	svc, ctx := managementService(t, &fakeOpening{}, &fakeOpening{})
	svc.management = nil

	_, err := svc.MutationCloseRestaurant(ctx, &service.MutationCloseRestaurantRequest{RestaurantId: nightBakery.ID})

	assert.Equal(t, codes.Unavailable, status.Code(err))
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
