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
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/market"
	kitchenapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
	kitcheninfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/infrastructure"
	trackingapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
	trackinginfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/infrastructure"
	managementapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
	managementinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/infrastructure"
	simulatorapp "github.com/batazor/nomnom/boundaries/bff/plugins/bff/simulator/applications"
	simulatorinfra "github.com/batazor/nomnom/boundaries/bff/plugins/bff/simulator/infrastructure"
)

// envInternalGateway is the address of the internal gateway (ADR 15) of the
// Cell this BFF serves (ADR 41).
const envInternalGateway = "BFF__INTERNAL_GATEWAY_ADDR"

// envCallTimeout is the upper bound of one call to a context through the
// internal gateway (ADR 15), a Go duration.
const envCallTimeout = "BFF__INTERNAL_GATEWAY_CALL_TIMEOUT"

// Start point of couriers a dispatcher adds on the flow board (FR-623): a new
// courier has no position of its own, and delivery offers a delivery only to
// couriers with one.
const (
	envCourierStartLatitude  = "BFF__COURIER_START_LATITUDE"
	envCourierStartLongitude = "BFF__COURIER_START_LONGITUDE"
)

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
	// callTimeout is the raw variable: newBffService validates it.
	callTimeout string
	// courierStartLatitude and courierStartLongitude are the raw variables:
	// newBffService validates them.
	courierStartLatitude  string
	courierStartLongitude string
}

func configFromEnv(getenv func(string) string) config {
	return config{
		identity:              identity.ConfigFromEnv(getenv),
		internalGateway:       getenv(envInternalGateway),
		callTimeout:           getenv(envCallTimeout),
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

// timeout validates the upper bound of one call to a context; it is required.
func (c config) timeout() (time.Duration, error) {
	timeout, err := time.ParseDuration(c.callTimeout)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", envCallTimeout, err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %s", envCallTimeout, timeout)
	}
	return timeout, nil
}

// dialFunc opens a connection to an internal gateway for one audience;
// gateway.Dial in production.
type dialFunc func(address, audience string, callTimeout time.Duration, credentials identity.OutgoingCredentials) (*grpc.ClientConn, error)

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

type listScheduledSlotsHandler interface {
	Handle(ctx context.Context, restaurantIDs []string) ([][]catalogapp.Slot, error)
}

// restaurantCatalog holds the scenarios of the catalog screens.
type restaurantCatalog struct {
	listRestaurants    listRestaurantsHandler
	getRestaurant      getRestaurantHandler
	listScheduledSlots listScheduledSlotsHandler
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
	setOpeningHours      setOpeningHoursHandler
}

type getKitchenHandler interface {
	Handle(ctx context.Context, restaurantID string) (kitchenapp.KitchenView, error)
}

type kitchenActionHandler interface {
	Handle(ctx context.Context, request kitchenapp.ActionRequest) (kitchenapp.ActionResult, error)
}

// kitchen holds the scenarios of the kitchen screen.
type startNextPreparationHandler interface {
	Handle(ctx context.Context, request kitchenapp.StartNextRequest) (kitchenapp.ActionResult, error)
}

type kitchen struct {
	getKitchen     getKitchenHandler
	applyAction    kitchenActionHandler
	startNext      startNextPreparationHandler
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

// connections are the connections of BFF to the contexts of its Cell, one
// per audience, all through the internal gateway of the Cell.
type connections []*grpc.ClientConn

// close closes every connection.
func (c connections) close() error {
	var errs []error
	for _, conn := range c {
		errs = append(errs, conn.Close())
	}
	return errors.Join(errs...)
}

// simulator holds the scenarios of the demo Simulator switch in the header.
type simulator struct {
	getSimulator        getSimulatorHandler
	setSimulatorEnabled setSimulatorEnabledHandler
}

// newBffService assembles BFF from cfg. BFF identity and the gateway address
// are required: customer operations must not run without a verified customer
// (ADR 32), and every slice calls contexts through the internal gateway of the
// Cell (ADR 41) with a bounded call time. Every call needs the Market of the request; one without it is
// refused before a context is called. Connections are lazy: no I/O here.
func newBffService(cfg config, dial dialFunc) (*BffService, error) {
	customers, err := identity.Load(cfg.identity)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}
	if cfg.internalGateway == "" {
		return nil, fmt.Errorf("%s is required", envInternalGateway)
	}
	callTimeout, err := cfg.timeout()
	if err != nil {
		return nil, err
	}
	courierStart, err := cfg.courierStart()
	if err != nil {
		return nil, err
	}
	var conns connections
	connect := func(audience string) (grpc.ClientConnInterface, error) {
		conn, err := dial(cfg.internalGateway, audience, callTimeout, customers.Outgoing)
		if err != nil {
			return nil, errors.Join(err, conns.close())
		}
		conns = append(conns, conn)
		return market.Require(conn), nil
	}
	orders, err := connect(audienceOrders)
	if err != nil {
		return nil, err
	}
	restaurants, err := connect(audienceRestaurants)
	if err != nil {
		return nil, err
	}
	delivery, err := connect(audienceDelivery)
	if err != nil {
		return nil, err
	}
	service := &BffService{accessTokens: customers.AccessTokens, conns: conns}
	service.tracking = newOrderTracking(orders, restaurants, delivery)
	service.catalog = newRestaurantCatalog(restaurants)
	// The payload of checkout reuses the GetOrder scenario of order tracking.
	service.checkout = newCheckout(orders, service.tracking.getOrder)
	// The payload of setOpeningHours reuses the GetRestaurant scenario of the catalog.
	service.management = newRestaurantManagement(restaurants, service.catalog.getRestaurant)
	service.kitchen = newKitchen(restaurants)
	service.courier = newCourier(delivery)
	service.courierPool = newCourierPool(delivery, courierStart)
	service.simulator = newSimulator(orders, restaurants, delivery)
	return service, nil
}

func newOrderTracking(orders, restaurants, delivery grpc.ClientConnInterface) *orderTracking {
	ordersAPI := trackinginfra.NewOrders(orders)
	catalog := trackinginfra.NewCatalog(restaurants)
	getOrder := trackingapp.NewGetOrder(ordersAPI, catalog, trackinginfra.NewDeliveries(delivery))
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
		startNext:      kitchenapp.NewStartNextPreparation(restaurantsAPI, restaurantsAPI, restaurantsAPI),
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
		listRestaurants:    catalogapp.NewListRestaurants(restaurantsAPI, restaurantsAPI),
		getRestaurant:      catalogapp.NewGetRestaurant(restaurantsAPI),
		listScheduledSlots: catalogapp.NewListScheduledSlots(restaurantsAPI),
	}
}

func newCheckout(orders grpc.ClientConnInterface, views checkoutapp.OrderViews) *checkout {
	return &checkout{placeOrder: checkoutapp.NewPlaceOrder(checkoutinfra.NewOrders(orders), views)}
}

func newRestaurantManagement(restaurants grpc.ClientConnInterface, views managementapp.RestaurantViews) *restaurantManagement {
	restaurantsAPI := managementinfra.NewRestaurants(restaurants)
	return &restaurantManagement{
		openRestaurant:       managementapp.NewOpenRestaurant(restaurantsAPI),
		closeRestaurant:      managementapp.NewCloseRestaurant(restaurantsAPI),
		listOwnedRestaurants: managementapp.NewListOwnedRestaurants(restaurantsAPI),
		setOpeningHours:      managementapp.NewSetOpeningHours(restaurantsAPI, views),
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
