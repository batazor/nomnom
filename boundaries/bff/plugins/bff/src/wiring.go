package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"google.golang.org/grpc"

	catalogapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	cataloginfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/infrastructure"
	checkoutapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	checkoutinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/infrastructure"
	courierapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	courierinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/infrastructure"
	poolapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/courierpool/applications"
	poolinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/courierpool/infrastructure"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/cellrouter"
	kitchenapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
	kitcheninfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/infrastructure"
	marketsapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/markets/applications"
	marketsinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/markets/infrastructure"
	trackingapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
	trackinginfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/infrastructure"
	managementapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
	managementinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/infrastructure"
	simulatorapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/simulator/applications"
	simulatorinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/simulator/infrastructure"
)

// envInternalGateway is the address of the internal gateway (ADR 15) through
// which BFF calls the control plane (ADR 41); the gateways of Cells come from
// its Placement Map.
const envInternalGateway = "BFF__INTERNAL_GATEWAY_ADDR"

// envPlacementTTL is how often the Placement Map is refreshed, a Go duration
// (ADR 41).
const envPlacementTTL = "BFF__PLACEMENT_TTL"

// Start point of couriers a dispatcher adds on the flow board (FR-623): a new
// courier has no position of its own, and delivery offers a delivery only to
// couriers with one.
const (
	envCourierStartLatitude  = "BFF__COURIER_START_LATITUDE"
	envCourierStartLongitude = "BFF__COURIER_START_LONGITUDE"
)

// Audiences of the bounded contexts BFF calls; one routed connection serves
// every slice that calls the context. The control plane is global and not
// routed.
const (
	audienceOrders       = "nomnom.orders"
	audienceRestaurants  = "nomnom.restaurants"
	audienceDelivery     = "nomnom.delivery"
	audienceControlPlane = "nomnom.controlplane"
)

// config is the environment of the plugin, read once at start.
type config struct {
	identity        identity.Config
	internalGateway string
	// placementTTL is the raw variable: newBffService validates it.
	placementTTL string
	// courierStartLatitude and courierStartLongitude are the raw variables:
	// newBffService validates them.
	courierStartLatitude  string
	courierStartLongitude string
}

func configFromEnv(getenv func(string) string) config {
	return config{
		identity:              identity.ConfigFromEnv(getenv),
		internalGateway:       getenv(envInternalGateway),
		placementTTL:          getenv(envPlacementTTL),
		courierStartLatitude:  getenv(envCourierStartLatitude),
		courierStartLongitude: getenv(envCourierStartLongitude),
	}
}

// courierStart validates the start point of new couriers; both variables are
// required.
func (c config) courierStart() (poolapp.Location, error) {
	latitude, err := strconv.ParseFloat(c.courierStartLatitude, 64)
	if err != nil {
		return poolapp.Location{}, fmt.Errorf("%s must be a number: %w", envCourierStartLatitude, err)
	}
	longitude, err := strconv.ParseFloat(c.courierStartLongitude, 64)
	if err != nil {
		return poolapp.Location{}, fmt.Errorf("%s must be a number: %w", envCourierStartLongitude, err)
	}
	location, err := poolapp.NewLocation(latitude, longitude)
	if err != nil {
		return poolapp.Location{}, fmt.Errorf("courier start point: %w", err)
	}
	return location, nil
}

// placementTTLDuration validates the refresh period of the Placement Map; it is
// required.
func (c config) placementTTLDuration() (time.Duration, error) {
	ttl, err := time.ParseDuration(c.placementTTL)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration: %w", envPlacementTTL, err)
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %s", envPlacementTTL, ttl)
	}
	return ttl, nil
}

// dialFunc opens a connection to an internal gateway for one audience;
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
	getKitchen     getKitchenHandler
	applyAction    kitchenActionHandler
	changeCookPool cookPoolHandler
}

type cookPoolHandler interface {
	Handle(ctx context.Context, request kitchenapp.CookPoolRequest) (kitchenapp.CookPoolResult, error)
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

// cellRouting is the cell router of BFF (ADR 41): the Placement Map refreshed
// in the background, the routed connections to Cells and the connection to
// the control plane.
type cellRouting struct {
	placements   *cellrouter.Placements
	router       *cellrouter.Router
	controlPlane *grpc.ClientConn
}

// start begins loading the Placement Map; requests before the first load are
// UNAVAILABLE.
func (c *cellRouting) start() { c.placements.Start() }

// stop ends the refresher and closes every connection.
func (c *cellRouting) stop() error {
	c.placements.Stop()
	return errors.Join(c.router.Close(), c.controlPlane.Close())
}

// simulator holds the scenarios of the demo Simulator switch in the header.
type simulator struct {
	getSimulator        getSimulatorHandler
	setSimulatorEnabled setSimulatorEnabledHandler
}

// newBffService assembles BFF from cfg. BFF identity, the gateway address and
// the Placement TTL are required: customer operations must not run without a
// verified customer (ADR 32), and every slice calls contexts through the
// gateway of the Cell of the request Market (ADR 41). Only the control plane
// connection is opened here; cell routing starts with cells.start.
func newBffService(cfg config, dial dialFunc) (*BffService, error) {
	customers, err := identity.Load(cfg.identity)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}
	if cfg.internalGateway == "" {
		return nil, fmt.Errorf("%s is required", envInternalGateway)
	}
	courierStart, err := cfg.courierStart()
	if err != nil {
		return nil, err
	}
	placementTTL, err := cfg.placementTTLDuration()
	if err != nil {
		return nil, err
	}
	controlPlane, err := dial(cfg.internalGateway, audienceControlPlane, customers.Outgoing)
	if err != nil {
		return nil, err
	}
	placements, err := cellrouter.NewPlacements(controlPlane, placementTTL)
	if err != nil {
		return nil, errors.Join(err, controlPlane.Close())
	}
	router := cellrouter.NewRouter(placements, func(address, audience string) (*grpc.ClientConn, error) {
		return dial(address, audience, customers.Outgoing)
	})
	service := &BffService{
		accessTokens: customers.AccessTokens,
		cells:        &cellRouting{placements: placements, router: router, controlPlane: controlPlane},
		listMarkets:  marketsapp.NewListMarkets(marketsinfra.NewControlPlane(controlPlane)),
	}
	orders := router.Conn(audienceOrders)
	restaurants := router.Conn(audienceRestaurants)
	delivery := router.Conn(audienceDelivery)
	service.tracking = newOrderTracking(orders, restaurants, delivery)
	service.catalog = newRestaurantCatalog(restaurants)
	// The payload of checkout reuses the GetOrder scenario of order tracking.
	service.checkout = newCheckout(orders, service.tracking.getOrder)
	service.management = newRestaurantManagement(restaurants)
	service.kitchen = newKitchen(restaurants)
	service.courier = newCourier(delivery)
	service.courierPool = newCourierPool(delivery, courierStart)
	service.simulator = newSimulator(orders, restaurants, delivery)
	return service, nil
}

func newOrderTracking(orders, restaurants, delivery grpc.ClientConnInterface) *orderTracking {
	ordersAPI := trackinginfra.NewOrders(orders)
	catalog := trackinginfra.NewCatalog(restaurants)
	getOrder := trackingapp.NewGetOrder(ordersAPI, catalog, trackinginfra.NewCouriers(delivery))
	return &orderTracking{
		getOrder:            getOrder,
		requestCancellation: trackingapp.NewRequestCancellation(ordersAPI),
		// Every Order of the history page is the GetOrder view.
		listCustomerOrders: trackingapp.NewListCustomerOrders(ordersAPI, catalog),
	}
}

func newKitchen(restaurants grpc.ClientConnInterface) *kitchen {
	restaurantsAPI := kitcheninfra.NewRestaurants(restaurants)
	return &kitchen{
		getKitchen:     kitchenapp.NewGetKitchen(restaurantsAPI, restaurantsAPI),
		applyAction:    kitchenapp.NewApplyKitchenAction(restaurantsAPI, restaurantsAPI, restaurantsAPI),
		changeCookPool: kitchenapp.NewChangeCookPool(restaurantsAPI, restaurantsAPI),
	}
}

func newCourierPool(delivery grpc.ClientConnInterface, start poolapp.Location) *courierPool {
	deliveryAPI := poolinfra.NewDelivery(delivery)
	return &courierPool{
		getPool:    poolapp.NewGetPool(deliveryAPI),
		changePool: poolapp.NewChangePool(deliveryAPI, newCourierID, start),
	}
}

// newCourierID names a courier added on the flow board: a UUIDv7, no user
// stands behind it.
func newCourierID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
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
		listRestaurants: catalogapp.NewListRestaurants(restaurantsAPI, restaurantsAPI),
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

func newSimulator(orders, restaurants, delivery grpc.ClientConnInterface) *simulator {
	simulators := []simulatorapp.Simulator{
		simulatorinfra.NewOrders(orders),
		simulatorinfra.NewRestaurants(restaurants),
		simulatorinfra.NewDelivery(delivery),
	}
	return &simulator{
		getSimulator:        simulatorapp.NewGetSimulator(simulators...),
		setSimulatorEnabled: simulatorapp.NewSetSimulatorEnabled(simulators...),
	}
}
