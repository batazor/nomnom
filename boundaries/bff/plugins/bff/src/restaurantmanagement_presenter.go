package main

import (
	"google.golang.org/protobuf/types/known/wrapperspb"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/restaurantmanagement/applications"
)

func restaurantOpeningPayload(result applications.OpeningResult) *service.RestaurantOpeningPayload {
	return &service.RestaurantOpeningPayload{
		Restaurant: managedRestaurantMessage(result.Restaurant),
		Changed:    result.Changed,
	}
}

func ownedRestaurantConnection(page applications.RestaurantPage) *service.RestaurantSummaryConnection {
	connection := &service.RestaurantSummaryConnection{
		Edges:    make([]*service.RestaurantSummaryEdge, 0, len(page.Edges)),
		PageInfo: &service.PageInfo{HasNextPage: page.HasNextPage},
	}
	if page.EndCursor != "" {
		connection.PageInfo.EndCursor = wrapperspb.String(page.EndCursor)
	}
	for _, edge := range page.Edges {
		connection.Edges = append(connection.Edges, &service.RestaurantSummaryEdge{
			Cursor: edge.Cursor,
			Node:   managedRestaurantMessage(edge.Restaurant),
		})
	}
	return connection
}

func managedRestaurantMessage(restaurant applications.Restaurant) *service.RestaurantSummary {
	return &service.RestaurantSummary{
		Id:              restaurant.ID,
		Name:            restaurant.Name,
		AcceptingOrders: restaurant.IsOpen,
	}
}
