package main

import (
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

func restaurantConnection(page applications.RestaurantPage) *service.RestaurantConnection {
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
			Node:   restaurantWithMenu(applications.RestaurantView{Restaurant: edge.Restaurant, Menu: edge.Menu}),
		})
	}
	return connection
}

// restaurantWithMenu leaves menu null for a restaurant without one.
func restaurantWithMenu(view applications.RestaurantView) *service.Restaurant {
	restaurant := restaurantMessage(view.Restaurant)
	if view.Menu != nil {
		restaurant.Menu = &service.Menu{Items: make([]*service.MenuItem, 0, len(view.Menu))}
		for _, item := range view.Menu {
			restaurant.Menu.Items = append(restaurant.Menu.Items, menuItemMessage(item))
		}
	}
	return restaurant
}

func restaurantMessage(restaurant applications.Restaurant) *service.Restaurant {
	return &service.Restaurant{
		Id:              restaurant.ID,
		Name:            restaurant.Name,
		Address:         restaurant.Address,
		AcceptingOrders: restaurant.IsOpen,
	}
}

func menuItemMessage(item applications.MenuItem) *service.MenuItem {
	message := &service.MenuItem{
		Id:        item.ID,
		Name:      item.Name,
		Price:     &service.Money{Amount: item.Price.Amount, Currency: item.Price.Currency},
		Available: item.Available,
	}
	if item.Description != "" {
		message.Description = wrapperspb.String(item.Description)
	}
	return message
}
