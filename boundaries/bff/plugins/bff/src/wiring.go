package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	catalogapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	cataloginfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/infrastructure"
	checkoutapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	checkoutinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/infrastructure"
	courierapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	courierinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/infrastructure"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	kitchenapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
	kitcheninfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/infrastructure"
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
	audienceDelivery    = "nomnom.delivery"
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

type listCustomerOrdersHandler interface {
	Handle(ctx context.Context, customerID string, first int, after string) (trackingapp.OrderPage, error)
}

// orderTracking holds the scenarios of the order tracking and order history
// screens.
type orderTracking struct {
	getOrder            getOrderHandler
	requestCancellation requestCancellationHandler
	listCustomerOrders  listCustomerOrdersHandler
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

type getKitchenHandler interface {
	Handle(ctx context.Context, restaurantID string) (kitchenapp.KitchenView, error)
}

type kitchenActionHandler interface {
	Handle(ctx context.Context, request kitchenapp.ActionRequest) (kitchenapp.ActionResult, error)
}

// kitchen holds the scenarios of the kitchen screen.
type kitchen struct {
	getKitchen  getKitchenHandler
	applyAction kitchenActionHandler
}

type getCourierHandler interface {
	Handle(ctx context.Context, courierID string) (courierapp.CourierView, error)
}

type setAvailabilityHandler interface {
	Handle(ctx context.Context, courierID string, availability courierapp.Availability) (courierapp.CommandResult, error)
}

type acceptOfferHandler interface {
	Handle(ctx context.Context, courierID, offerID string) (courierapp.CommandResult, error)
}

type deliveryActionHandler interface {
	Handle(ctx context.Context, courierID, deliveryID string, action courierapp.Action) (courierapp.CommandResult, error)
}

// courier holds the scenarios of the courier screen.
type courier struct {
	getCourier      getCourierHandler
	setAvailability setAvailabilityHandler
	acceptOffer     acceptOfferHandler
	applyAction     deliveryActionHandler
}

// newBffService assembles BFF from cfg. BFF identity and the gateway address
// are required: customer operations must not run without a verified customer
// (ADR 32), and every slice calls contexts through the internal gateway.
func newBffService(cfg config, dial dialFunc) (*BffService, error) {
	customers, err := identity.Load(cfg.identity)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}
	if cfg.internalGateway == "" {
		return nil, fmt.Errorf("%s is required", envInternalGateway)
	}
	service := &BffService{accessTokens: customers.AccessTokens}
	orders, err := dial(cfg.internalGateway, audienceOrders, customers.Outgoing)
	if err != nil {
		return nil, err
	}
	restaurants, err := dial(cfg.internalGateway, audienceRestaurants, customers.Outgoing)
	if err != nil {
		return nil, err
	}
	delivery, err := dial(cfg.internalGateway, audienceDelivery, customers.Outgoing)
	if err != nil {
		return nil, err
	}
	service.tracking = newOrderTracking(orders, restaurants)
	service.catalog = newRestaurantCatalog(restaurants)
	// The payload of checkout reuses the GetOrder scenario of order tracking.
	service.checkout = newCheckout(orders, service.tracking.getOrder)
	service.management = newRestaurantManagement(restaurants)
	service.kitchen = newKitchen(restaurants)
	service.courier = newCourier(delivery)
	return service, nil
}

func newOrderTracking(orders, restaurants grpc.ClientConnInterface) *orderTracking {
	ordersAPI := trackinginfra.NewOrders(orders)
	getOrder := trackingapp.NewGetOrder(ordersAPI, trackinginfra.NewCatalog(restaurants))
	return &orderTracking{
		getOrder:            getOrder,
		requestCancellation: trackingapp.NewRequestCancellation(ordersAPI),
		// Every Order of the history page is the GetOrder view.
		listCustomerOrders: trackingapp.NewListCustomerOrders(ordersAPI, getOrder),
	}
}

func newKitchen(restaurants grpc.ClientConnInterface) *kitchen {
	restaurantsAPI := kitcheninfra.NewRestaurants(restaurants)
	return &kitchen{
		getKitchen:  kitchenapp.NewGetKitchen(restaurantsAPI, restaurantsAPI),
		applyAction: kitchenapp.NewApplyKitchenAction(restaurantsAPI, restaurantsAPI, restaurantsAPI),
	}
}

func newCourier(delivery grpc.ClientConnInterface) *courier {
	deliveryAPI := courierinfra.NewDelivery(delivery)
	return &courier{
		getCourier:      courierapp.NewGetCourier(deliveryAPI),
		setAvailability: courierapp.NewSetAvailability(deliveryAPI),
		acceptOffer:     courierapp.NewAcceptOffer(deliveryAPI),
		applyAction:     courierapp.NewApplyDeliveryAction(deliveryAPI),
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
