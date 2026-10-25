package main

import (
	"google.golang.org/protobuf/types/known/wrapperspb"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
)

// restaurantOpeningPayload leaves menu null: the commands do not read it.
func restaurantOpeningPayload(result applications.OpeningResult) *service.RestaurantOpeningPayload {
	return &service.RestaurantOpeningPayload{
		Restaurant: managedRestaurantMessage(result.Restaurant),
		Changed:    result.Changed,
	}
}

// ownedRestaurantConnection leaves menu null: the list does not read it.
func ownedRestaurantConnection(page applications.RestaurantPage) *service.RestaurantConnection {
	connection := &service.RestaurantConnection{
		Edges:    make([]*service.RestaurantEdge, 0, len(page.Edges)),
		PageInfo: &service.PageInfo{HasNextPage: page.HasNextPage},
	}
	if page.EndCursor != "" {
		connection.PageInfo.EndCursor = wrapperspb.String(page.EndCursor)
	}
	for _, edge := range page.Edges {
		connection.Edges = append(connection.Edges, &service.RestaurantEdge{
			Cursor: edge.Cursor,
			Node:   managedRestaurantMessage(edge.Restaurant),
		})
	}
	return connection
}

func managedRestaurantMessage(restaurant applications.Restaurant) *service.Restaurant {
	return &service.Restaurant{
		Id:              restaurant.ID,
		Name:            restaurant.Name,
		Address:         restaurant.Address,
		AcceptingOrders: restaurant.IsOpen,
	}
}
