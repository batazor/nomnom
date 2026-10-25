package main

import (
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
)

// restaurantOpeningPayload leaves menu null: the commands do not read it.
func restaurantOpeningPayload(result applications.OpeningResult) *service.RestaurantOpeningPayload {
	return &service.RestaurantOpeningPayload{
		Restaurant: &service.Restaurant{
			Id:              result.Restaurant.ID,
			Name:            result.Restaurant.Name,
			Address:         result.Restaurant.Address,
			AcceptingOrders: result.Restaurant.IsOpen,
		},
		Changed: result.Changed,
	}
}
