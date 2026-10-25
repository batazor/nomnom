package main

import (
	"context"
	"fmt"
	"log"

	"google.golang.org/grpc"

	catalogapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	cataloginfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/infrastructure"
	checkoutapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	checkoutinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/infrastructure"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	trackingapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
	trackinginfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/infrastructure"
	managementapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
	managementinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/infrastructure"
)

// envInternalGateway is the address of the internal gateway (ADR 15) through
// which BFF calls bounded contexts.
const envInternalGateway = "BFF__INTERNAL_GATEWAY_ADDR"

// Audiences of the bounded contexts BFF calls; one connection serves every
// slice that calls the context.
const (
	audienceOrders      = "nomnom.orders"
	audienceRestaurants = "nomnom.restaurants"
)

// config is the environment of the plugin, read once at start.
type config struct {
	identity        identity.Config
	internalGateway string
}

func configFromEnv(getenv func(string) string) config {
	return config{
		identity:        identity.ConfigFromEnv(getenv),
		internalGateway: getenv(envInternalGateway),
	}
}

// dialFunc opens a connection to the internal gateway for one audience;
// gateway.Dial in production.
type dialFunc func(address, audience string, credentials identity.OutgoingCredentials) (*grpc.ClientConn, error)

type getOrderHandler interface {
	Handle(ctx context.Context, orderID string) (trackingapp.OrderView, error)
}

type requestCancellationHandler interface {
	Handle(ctx context.Context, request trackingapp.CancellationRequest) (trackingapp.CancellationResult, error)
}

// orderTracking holds the scenarios of the order tracking screen.
type orderTracking struct {
	getOrder            getOrderHandler
	requestCancellation requestCancellationHandler
}

type listRestaurantsHandler interface {
	Handle(ctx context.Context, first int, after string) (catalogapp.RestaurantPage, error)
}

type getRestaurantHandler interface {
	Handle(ctx context.Context, restaurantID string) (catalogapp.RestaurantView, error)
}

// restaurantCatalog holds the scenarios of the catalog screens.
type restaurantCatalog struct {
	listRestaurants listRestaurantsHandler
	getRestaurant   getRestaurantHandler
}

type placeOrderHandler interface {
	Handle(ctx context.Context, input checkoutapp.Input) (checkoutapp.Result, error)
}

// checkout holds the scenario of the checkout screen.
type checkout struct {
	placeOrder placeOrderHandler
}

// restaurantManagement holds the scenarios of the restaurant owner screen.
type restaurantManagement struct {
	openRestaurant       openingHandler
	closeRestaurant      openingHandler
	listOwnedRestaurants ownedRestaurantsHandler
}

// newBffService assembles BFF from cfg. A partial identity configuration is an
// error: customer operations must not run without a verified customer
// (ADR 32). Without BFF identity or a gateway address the slices stay nil:
// their resolvers answer that they are not available, while bffInfo keeps
// working. The catalog reads are public, but the internal gateway still needs
// the service token of BFF, so the catalog needs identity too.
func newBffService(cfg config, dial dialFunc) (*BffService, error) {
	customers, err := identity.Load(cfg.identity)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}
	service := &BffService{}
	if customers != nil {
		service.accessTokens = customers.AccessTokens
	}
	if customers == nil || cfg.internalGateway == "" {
		log.Printf("%s or BFF identity is not set: catalog, checkout, order and restaurant management operations are unavailable", envInternalGateway)
		return service, nil
	}
	orders, err := dial(cfg.internalGateway, audienceOrders, customers.Outgoing)
	if err != nil {
		return nil, err
	}
	restaurants, err := dial(cfg.internalGateway, audienceRestaurants, customers.Outgoing)
	if err != nil {
		return nil, err
	}
	service.tracking = newOrderTracking(orders, restaurants)
	service.catalog = newRestaurantCatalog(restaurants)
	// The payload of checkout reuses the GetOrder scenario of order tracking.
	service.checkout = newCheckout(orders, service.tracking.getOrder)
	service.management = newRestaurantManagement(restaurants)
	return service, nil
}

func newOrderTracking(orders, restaurants grpc.ClientConnInterface) *orderTracking {
	ordersAPI := trackinginfra.NewOrders(orders)
	return &orderTracking{
		getOrder:            trackingapp.NewGetOrder(ordersAPI, trackinginfra.NewCatalog(restaurants)),
		requestCancellation: trackingapp.NewRequestCancellation(ordersAPI),
	}
}

func newRestaurantCatalog(restaurants grpc.ClientConnInterface) *restaurantCatalog {
	restaurantsAPI := cataloginfra.NewRestaurants(restaurants)
	return &restaurantCatalog{
		listRestaurants: catalogapp.NewListRestaurants(restaurantsAPI),
		getRestaurant:   catalogapp.NewGetRestaurant(restaurantsAPI),
	}
}

func newCheckout(orders grpc.ClientConnInterface, views checkoutapp.OrderViews) *checkout {
	return &checkout{placeOrder: checkoutapp.NewPlaceOrder(checkoutinfra.NewOrders(orders), views)}
}

func newRestaurantManagement(restaurants grpc.ClientConnInterface) *restaurantManagement {
	restaurantsAPI := managementinfra.NewRestaurants(restaurants)
	return &restaurantManagement{
		openRestaurant:       managementapp.NewOpenRestaurant(restaurantsAPI),
		closeRestaurant:      managementapp.NewCloseRestaurant(restaurantsAPI),
		listOwnedRestaurants: managementapp.NewListOwnedRestaurants(restaurantsAPI),
	}
}
