package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	courierapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	kitchenapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
)

// ADR 36: a courierChanged event resolves only the courier of the access
// token; a foreign key is NOT_FOUND, never the work of another courier.
func TestLookupCourierByIdIsTheVerifiedCourierOnly(t *testing.T) {
	fake := &fakeCourierScenarios{view: busyCourier}
	svc, ctx := courierService(t, fake)
	query, err := svc.QueryCourier(ctx, &service.QueryCourierRequest{})
	require.NoError(t, err)
	courierID := query.GetCourier().GetId()
	require.NotEmpty(t, courierID, "the courier carries its entity key")

	response, err := svc.LookupCourierById(ctx, &service.LookupCourierByIdRequest{
		Keys: []*service.LookupCourierByIdRequestKey{{Id: courierID}},
	})
	require.NoError(t, err)
	require.Len(t, response.GetResult(), 1)
	assert.Equal(t, courierID, response.GetResult()[0].GetId())

	_, err = svc.LookupCourierById(ctx, &service.LookupCourierByIdRequest{
		Keys: []*service.LookupCourierByIdRequestKey{{Id: "someone-else"}},
	})
	assert.Equal(t, codes.NotFound, status.Code(err))

	fake.err = courierapp.ErrNotCourier
	_, err = svc.LookupCourierById(ctx, &service.LookupCourierByIdRequest{
		Keys: []*service.LookupCourierByIdRequestKey{{Id: courierID}},
	})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ADR 36: a kitchenChanged event resolves the board with the subscriber's
// rights; a kitchen the user may not see is NOT_FOUND.
func TestLookupKitchenByRestaurantIdResolvesAsTheUser(t *testing.T) {
	get := &fakeGetKitchen{view: kitchenapp.KitchenView{Restaurant: kitchenapp.Restaurant{ID: "restaurant-1", Name: "Кухня №8"}}}
	svc, ctx := kitchenService(t, get, &fakeKitchenAction{})
	request := &service.LookupKitchenByRestaurantIdRequest{
		Keys: []*service.LookupKitchenByRestaurantIdRequestKey{{RestaurantId: "restaurant-1"}},
	}

	response, err := svc.LookupKitchenByRestaurantId(ctx, request)
	require.NoError(t, err)
	require.Len(t, response.GetResult(), 1)
	assert.Equal(t, "restaurant-1", response.GetResult()[0].GetRestaurantId())
	assert.True(t, get.hasUser, "restaurants is called on behalf of the user")

	get.err = kitchenapp.ErrKitchenNotFound
	_, err = svc.LookupKitchenByRestaurantId(ctx, request)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
