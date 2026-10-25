package main

import (
	"context"
	"errors"
	"log"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/infrastructure"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/gateway"
)

type listRestaurantsHandler interface {
	Handle(ctx context.Context, first int, after string) (applications.RestaurantPage, error)
}

type getRestaurantHandler interface {
	Handle(ctx context.Context, restaurantID string) (applications.RestaurantView, error)
}

// restaurantCatalog holds the scenarios of the catalog screens.
type restaurantCatalog struct {
	listRestaurants listRestaurantsHandler
	getRestaurant   getRestaurantHandler
}

// newRestaurantCatalog connects the slice to restaurants. The reads are public,
// but the internal gateway still needs the service token of BFF, so without
// BFF identity or a gateway address it returns nil and the catalog resolvers
// answer that they are not available.
func newRestaurantCatalog(customers *identity.Identity, getenv func(string) string) (*restaurantCatalog, error) {
	address := getenv(envInternalGateway)
	if customers == nil || address == "" {
		log.Printf("%s or BFF identity is not set: catalog operations are unavailable", envInternalGateway)
		return nil, nil
	}
	conn, err := gateway.Dial(address, "nomnom.restaurants", customers.Outgoing)
	if err != nil {
		return nil, err
	}
	restaurants := infrastructure.NewRestaurants(conn)
	return &restaurantCatalog{
		listRestaurants: applications.NewListRestaurants(restaurants),
		getRestaurant:   applications.NewGetRestaurant(restaurants),
	}, nil
}

func loadRestaurantCatalog(customers *identity.Identity) *restaurantCatalog {
	catalog, err := newRestaurantCatalog(customers, os.Getenv)
	if err != nil {
		log.Fatalf("connect catalog: %v", err)
	}
	return catalog
}

// QueryRestaurants is public: no access token is required (FR-499).
func (s *BffService) QueryRestaurants(ctx context.Context, request *service.QueryRestaurantsRequest) (*service.QueryRestaurantsResponse, error) {
	if s.catalog == nil {
		return nil, status.Error(codes.Unavailable, "catalog operations are not available")
	}
	page, err := s.catalog.listRestaurants.Handle(ctx, int(request.GetFirst()), request.GetAfter().GetValue())
	if errors.Is(err, applications.ErrInvalidPageSize) {
		return nil, status.Error(codes.InvalidArgument, "first must be between 1 and 100")
	}
	if err != nil {
		return nil, internalError("restaurants", err)
	}
	connection := &service.RestaurantConnection{
		Edges:    make([]*service.RestaurantEdge, 0, len(page.Edges)),
		PageInfo: &service.PageInfo{HasNextPage: page.HasNextPage},
	}
	if page.EndCursor != "" {
		connection.PageInfo.EndCursor = wrapperspb.String(page.EndCursor)
	}
	for _, edge := range page.Edges {
		// menu is not filled in the list (design decision 4).
		connection.Edges = append(connection.Edges, &service.RestaurantEdge{
			Cursor: edge.Cursor,
			Node:   restaurantMessage(edge.Restaurant),
		})
	}
	return &service.QueryRestaurantsResponse{Restaurants: connection}, nil
}

// QueryRestaurant is public; an unknown restaurant is null.
func (s *BffService) QueryRestaurant(ctx context.Context, request *service.QueryRestaurantRequest) (*service.QueryRestaurantResponse, error) {
	if s.catalog == nil {
		return nil, status.Error(codes.Unavailable, "catalog operations are not available")
	}
	view, err := s.catalog.getRestaurant.Handle(ctx, request.GetId())
	if errors.Is(err, applications.ErrRestaurantNotFound) {
		return &service.QueryRestaurantResponse{}, nil
	}
	if err != nil {
		return nil, internalError("restaurant", err)
	}
	restaurant := restaurantMessage(view.Restaurant)
	if view.Menu != nil {
		restaurant.Menu = &service.Menu{Items: make([]*service.MenuItem, 0, len(view.Menu))}
		for _, item := range view.Menu {
			restaurant.Menu.Items = append(restaurant.Menu.Items, menuItemMessage(item))
		}
	}
	return &service.QueryRestaurantResponse{Restaurant: restaurant}, nil
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
