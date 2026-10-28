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
	restaurant.WaitTime = &service.RestaurantWaitTime{
		Elevated:            view.Restaurant.WaitTime.Elevated,
		ExpectedWaitMinutes: int32(view.Restaurant.WaitTime.ExpectedWaitMinutes),
	}
	if fee := view.Restaurant.PremiumFee; fee != nil {
		restaurant.PremiumFee = &service.Money{Amount: fee.Amount, Currency: fee.Currency}
	}
	if view.Menu != nil {
		restaurant.Menu = &service.Menu{Items: make([]*service.MenuItem, 0, len(view.Menu))}
		for _, item := range view.Menu {
			restaurant.Menu.Items = append(restaurant.Menu.Items, menuItemMessage(item))
		}
	}
	return restaurant
}

func restaurantMessage(restaurant applications.Restaurant) *service.Restaurant {
	message := &service.Restaurant{
		Id:              restaurant.ID,
		Name:            restaurant.Name,
		AcceptingOrders: restaurant.AcceptingOrders,
		TimeZone:        restaurant.TimeZone,
		OpeningHours:    make([]*service.OpeningInterval, 0, len(restaurant.OpeningHours)),
		NextOpeningAt:   deadline(restaurant.NextOpeningAt),
	}
	for _, interval := range restaurant.OpeningHours {
		message.OpeningHours = append(message.OpeningHours, &service.OpeningInterval{
			DayOfWeek: int32(interval.Day),
			Opens:     interval.Opens,
			Closes:    interval.Closes,
		})
	}
	return message
}

func deliveryWindowMessage(slot applications.Slot) *service.DeliveryWindow {
	return &service.DeliveryWindow{Start: timestamp(slot.Start), End: timestamp(slot.End), Available: slot.Available}
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
