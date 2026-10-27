package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	courierapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	kitchenapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
)

// ADR 36: a courierChanged event resolves only the courier of the access
// token; a foreign key is NOT_FOUND, never the work of another courier.
func TestLookupCourierByIdIsTheVerifiedCourierOnly(t *testing.T) {
	getCourier := newMockGetCourierHandler(t)
	getCourier.EXPECT().Handle(mock.Anything, mock.MatchedBy(func(courierID string) bool { return courierID != "" })).Return(busyCourier, nil).Twice()
	getCourier.EXPECT().Handle(mock.Anything, mock.MatchedBy(func(courierID string) bool { return courierID != "" })).Return(courierapp.CourierView{}, courierapp.ErrNotCourier).Once()
	svc, ctx := courierService(t, getCourier, newMockSetAvailabilityHandler(t), newMockAcceptOfferHandler(t), newMockDeliveryActionHandler(t))
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

	_, err = svc.LookupCourierById(ctx, &service.LookupCourierByIdRequest{
		Keys: []*service.LookupCourierByIdRequestKey{{Id: courierID}},
	})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ADR 36: a kitchenChanged event resolves the board with the subscriber's
// rights; a kitchen the user may not see is NOT_FOUND.
func TestLookupKitchenByRestaurantIdResolvesAsTheUser(t *testing.T) {
	get := newMockGetKitchenHandler(t)
	// restaurants is called on behalf of the user.
	get.EXPECT().Handle(mock.MatchedBy(func(ctx context.Context) bool { user, ok := identity.UserFrom(ctx); return ok && user.ID != "" }), "restaurant-1").Return(kitchenapp.KitchenView{Restaurant: kitchenapp.Restaurant{ID: "restaurant-1", Name: "Кухня №8"}}, nil).Once()
	get.EXPECT().Handle(mock.MatchedBy(func(ctx context.Context) bool { user, ok := identity.UserFrom(ctx); return ok && user.ID != "" }), "restaurant-1").Return(kitchenapp.KitchenView{}, kitchenapp.ErrKitchenNotFound).Once()
	svc, ctx := kitchenService(t, get, newMockKitchenActionHandler(t))
	request := &service.LookupKitchenByRestaurantIdRequest{
		Keys: []*service.LookupKitchenByRestaurantIdRequestKey{{RestaurantId: "restaurant-1"}},
	}

	response, err := svc.LookupKitchenByRestaurantId(ctx, request)
	require.NoError(t, err)
	require.Len(t, response.GetResult(), 1)
	assert.Equal(t, "restaurant-1", response.GetResult()[0].GetRestaurantId())

	_, err = svc.LookupKitchenByRestaurantId(ctx, request)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
