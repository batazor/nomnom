package grpcadapter_test

import (
	"context"
	"fmt"
	"net"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	grpcadapter "nomnom/orders/infrastructure/grpc"
	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/closure/requestordercancellation"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/placement/placeorder"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
	"nomnom/orders/slices/order/infrastructure/spicedb"
	"nomnom/orders/slices/simulator/applications/switchsimulator"
	simulatorrepository "nomnom/orders/slices/simulator/infrastructure/repository"
)

const cancellationWindow = 5 * time.Minute

// newClient serves the orders API whose authorizer, catalog and workflow
// expect no call.
func newClient(t *testing.T, subjects grpcadapter.SubjectResolver) ordersv1.OrdersServiceClient {
	t.Helper()
	return newClientWith(t, subjects, repositorytest.Open(t), NewMockAuthorizer(t))
}

// newClientWith serves the orders API over store and authorizer; its catalog
// and workflow expect no call.
func newClientWith(t *testing.T, subjects grpcadapter.SubjectResolver, store *repositorytest.Store, authorizer *MockAuthorizer) ordersv1.OrdersServiceClient {
	t.Helper()
	return newClientWithPorts(t, subjects, store, authorizer, commandPorts{
		catalog:  NewMockCatalog(t),
		workflow: NewMockCancellationRequester(t),
		clock:    fake.NewClock(ordertest.PlacedAt),
	})
}

// commandPorts are the ports of the command use cases besides store and authorizer.
type commandPorts struct {
	catalog  placeorder.Catalog
	slots    placeorder.SlotReservations
	workflow fulfillment.CancellationRequester
	clock    *fake.Clock
}

func newClientWithPorts(t *testing.T, subjects grpcadapter.SubjectResolver, store *repositorytest.Store, authorizer *MockAuthorizer, ports commandPorts) ordersv1.OrdersServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	module := orderdi.NewModule(orderdi.Dependencies{
		Commands:   store,
		ReadModels: store,
		Authorizer: authorizer,
		Policy:     fixedWindow(t, cancellationWindow),
		Catalog:    ports.catalog,
		Slots:      ports.slots,
		Workflow:   ports.workflow,
		Clock:      ports.clock,
		IDs:        &fake.IDs{},
		Telemetry:  observability.UseCases(),
	})
	handlers := grpcadapter.OrderHandlers{
		PlaceOrder:               module.PlaceOrder,
		RequestOrderCancellation: module.RequestOrderCancellation,
		GetOrder:                 module.GetOrder,
		ListCustomerOrders:       module.ListCustomerOrders,
	}
	ordersv1.RegisterOrdersServiceServer(server, grpcadapter.NewService(handlers,
		switchsimulator.NewHandlers(authorizer, simulatorrepository.NewState(store.Pool())), subjects))
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return ordersv1.NewOrdersServiceClient(connection)
}

func TestListCustomerOrdersCallsApplicationHandler(t *testing.T) {
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(authz.Subject{
		Kind: authz.SubjectUser,
		ID:   "customer-1",
	}, nil).Once()
	client := newClient(t, subjects)

	response, err := client.ListCustomerOrders(context.Background(), &ordersv1.ListCustomerOrdersRequest{
		CustomerId: "customer-1",
		MarketId:   "berlin",
	})

	require.NoError(t, err)
	assert.Empty(t, response.GetOrders())
	assert.Empty(t, response.GetNextPageToken())
}

// ADR 41: the history is per Market; every Order carries its market_id.
func TestListCustomerOrdersReturnsOnlyTheRequestedMarket(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-berlin")))
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-hamburg", ordertest.WithMarket("hamburg"))))
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
	client := newClientWith(t, subjects, store, NewMockAuthorizer(t))

	response, err := client.ListCustomerOrders(context.Background(), &ordersv1.ListCustomerOrdersRequest{
		CustomerId: "customer-1",
		MarketId:   "hamburg",
	})

	require.NoError(t, err)
	require.Len(t, response.GetOrders(), 1)
	assert.Equal(t, "order-hamburg", response.GetOrders()[0].GetOrderId())
	assert.Equal(t, "hamburg", response.GetOrders()[0].GetOrder().GetMarketId())
}

// ADR 41: market_id is required and must be a Market slug.
func TestListCustomerOrdersRequiresMarket(t *testing.T) {
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Times(2)
	client := newClient(t, subjects)
	for name, market := range map[string]string{"missing": "", "malformed": "Berlin"} {
		t.Run(name, func(t *testing.T) {
			_, err := client.ListCustomerOrders(context.Background(), &ordersv1.ListCustomerOrdersRequest{
				CustomerId: "customer-1",
				MarketId:   market,
			})
			requireReason(t, err, codes.InvalidArgument, "INVALID_ARGUMENT")
		})
	}
}

func TestServiceReturnsStructuredInvalidArgument(t *testing.T) {
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(authz.Subject{
		Kind: authz.SubjectUser,
		ID:   "customer-1",
	}, nil).Once()
	client := newClient(t, subjects)

	_, err := client.ListCustomerOrders(context.Background(), &ordersv1.ListCustomerOrdersRequest{})
	require.Error(t, err)
	st := status.Convert(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	details := st.Details()
	require.Len(t, details, 1)
	info, ok := details[0].(*errdetails.ErrorInfo)
	require.True(t, ok)
	assert.Equal(t, "INVALID_ARGUMENT", info.GetReason())
	assert.Equal(t, "nomnom.orders.v1", info.GetDomain())
}

func TestServiceRejectsUnverifiedCaller(t *testing.T) {
	// The resolver fails the call as an unverified caller.
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(authz.Subject{}, grpcadapter.ErrUnauthenticated).Once()
	client := newClient(t, subjects)

	_, err := client.ListCustomerOrders(context.Background(), &ordersv1.ListCustomerOrdersRequest{
		CustomerId: "customer-1",
		MarketId:   "berlin",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// AC-504, AC-505: GetOrder carries the window only while the Order may be cancelled.
func TestGetOrderMapsCancellationWindow(t *testing.T) {
	store := repositorytest.Open(t)
	authorizer := NewMockAuthorizer(t)
	for id, state := range map[order.ID]order.State{"order-placed": order.StatePlaced, "order-completed": order.StateCompleted} {
		o := ordertest.New(t, id, ordertest.WithState(state))
		require.NoError(t, store.Add(context.Background(), o))
	}
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(authz.Subject{
		Kind: authz.SubjectUser,
		ID:   "customer-1",
	}, nil).Times(2)
	client := newClientWith(t, subjects, store, authorizer)

	placed, err := client.GetOrder(context.Background(), &ordersv1.GetOrderRequest{OrderId: "order-placed"})
	require.NoError(t, err)
	window := placed.GetOrder().GetCancellationWindow()
	require.NotNil(t, window)
	assert.Equal(t, ordertest.PlacedAt, window.GetOpensAt().AsTime())
	assert.Equal(t, ordertest.PlacedAt.Add(cancellationWindow), window.GetClosesAt().AsTime())

	completed, err := client.GetOrder(context.Background(), &ordersv1.GetOrderRequest{OrderId: "order-completed"})
	require.NoError(t, err)
	assert.Nil(t, completed.GetOrder().GetCancellationWindow())
}

// FR-611: GetOrder returns the named deadlines and leaves an unnamed one empty.
func TestGetOrderMapsExpectedTimes(t *testing.T) {
	store := repositorytest.Open(t)
	authorizer := NewMockAuthorizer(t)
	expected := order.ExpectedTimes{ReadyAt: ordertest.PlacedAt.Add(20 * time.Minute), PickupAt: ordertest.PlacedAt.Add(25 * time.Minute)}
	o := ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed), ordertest.WithExpectedTimes(expected))
	require.NoError(t, store.Add(context.Background(), o))
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
	client := newClientWith(t, subjects, store, authorizer)

	response, err := client.GetOrder(context.Background(), &ordersv1.GetOrderRequest{OrderId: "order-1"})

	require.NoError(t, err)
	assert.Equal(t, ordertest.Market, response.GetOrder().GetMarketId())
	assert.Equal(t, expected.ReadyAt, response.GetOrder().GetExpectedReadyAt().AsTime())
	assert.Equal(t, expected.PickupAt, response.GetOrder().GetExpectedPickupAt().AsTime())
	assert.Nil(t, response.GetOrder().GetExpectedDeliveredAt())
}

// FR-681: GetOrder carries the Delivery Window of a Scheduled Order and the
// cancellation window closing CANCELLATION__SCHEDULED_CUTOFF before it.
func TestGetOrderMapsDeliveryWindow(t *testing.T) {
	store := repositorytest.Open(t)
	windowStart := ordertest.PlacedAt.Add(7 * time.Hour)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1",
		ordertest.WithDeliveryWindow(t, windowStart, windowStart.Add(15*time.Minute)))))
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-2")))
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Times(2)
	client := newClientWith(t, subjects, store, NewMockAuthorizer(t))

	scheduled, err := client.GetOrder(context.Background(), &ordersv1.GetOrderRequest{OrderId: "order-1"})
	require.NoError(t, err)
	assert.Equal(t, windowStart, scheduled.GetOrder().GetDeliveryWindow().GetStart().AsTime())
	assert.Equal(t, windowStart.Add(15*time.Minute), scheduled.GetOrder().GetDeliveryWindow().GetEnd().AsTime())
	assert.Equal(t, windowStart.Add(-time.Hour), scheduled.GetOrder().GetCancellationWindow().GetClosesAt().AsTime())

	asap, err := client.GetOrder(context.Background(), &ordersv1.GetOrderRequest{OrderId: "order-2"})
	require.NoError(t, err)
	assert.Nil(t, asap.GetOrder().GetDeliveryWindow())
}

// AC-912, AC-913: PlaceOrder reserves the window of delivery_window_start;
// a full window is SLOT_UNAVAILABLE with the alternatives in a SlotUnavailable
// detail.
func TestPlaceOrderWithDeliveryWindow(t *testing.T) {
	windowStart := ordertest.PlacedAt.Add(7 * time.Hour)
	window, err := order.NewDeliveryWindow(windowStart, windowStart.Add(15*time.Minute))
	require.NoError(t, err)
	alternative, err := order.NewDeliveryWindow(windowStart.Add(15*time.Minute), windowStart.Add(30*time.Minute))
	require.NoError(t, err)
	store, catalog, slots := repositorytest.Open(t), NewMockCatalog(t), NewMockSlotReservations(t)
	catalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza"}).Return(placeorder.CatalogSnapshot{
		MarketID:     ordertest.MarketID(t),
		Currency:     "EUR",
		Items:        map[order.MenuItemID]placeorder.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}},
		MinimumTotal: ordertest.Money(t, "0"),
		MaximumTotal: ordertest.Money(t, "100"),
	}, nil).Times(2)
	slots.EXPECT().Reserve(mock.Anything, order.RestaurantID("restaurant-1"), windowStart, order.ID("order-1")).Return(window, nil).Once()
	slots.EXPECT().Reserve(mock.Anything, order.RestaurantID("restaurant-1"), windowStart, order.ID("order-2")).
		Return(order.DeliveryWindow{}, placeorder.SlotUnavailable{Alternatives: []order.DeliveryWindow{alternative}}).Once()
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Times(2)
	client := newClientWithPorts(t, subjects, store, NewMockAuthorizer(t), commandPorts{
		catalog:  catalog,
		slots:    slots,
		workflow: NewMockCancellationRequester(t),
		clock:    fake.NewClock(ordertest.PlacedAt),
	})
	request := placeRequest()
	request.DeliveryWindowStart = timestamppb.New(windowStart)

	_, err = client.PlaceOrder(context.Background(), request)
	require.NoError(t, err)
	snapshot, ok := store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, window, snapshot.DeliveryWindow)

	request.IdempotencyKey = "key-2"
	_, err = client.PlaceOrder(context.Background(), request)
	st := status.Convert(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	require.Len(t, st.Details(), 2)
	info, ok := st.Details()[0].(*errdetails.ErrorInfo)
	require.True(t, ok)
	assert.Equal(t, "SLOT_UNAVAILABLE", info.GetReason())
	detail, ok := st.Details()[1].(*ordersv1.SlotUnavailable)
	require.True(t, ok)
	require.Len(t, detail.GetAlternatives(), 1)
	assert.Equal(t, alternative.Start(), detail.GetAlternatives()[0].GetStart().AsTime())
	assert.Equal(t, alternative.End(), detail.GetAlternatives()[0].GetEnd().AsTime())
}

// FR-650: GetOrder carries the priority and the premium fee of the Order.
func TestGetOrderMapsPremiumPriority(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithPremium(t, "1.50"))))
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-2")))
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Times(2)
	client := newClientWith(t, subjects, store, NewMockAuthorizer(t))

	premium, err := client.GetOrder(context.Background(), &ordersv1.GetOrderRequest{OrderId: "order-1"})
	require.NoError(t, err)
	assert.Equal(t, ordersv1.OrderPriority_ORDER_PRIORITY_PREMIUM, premium.GetOrder().GetPriority())
	assert.Equal(t, "1.5", premium.GetOrder().GetPremiumFee().GetAmount())
	assert.Equal(t, "21.5", premium.GetOrder().GetTotal().GetAmount(), "the fee is part of the total")

	standard, err := client.GetOrder(context.Background(), &ordersv1.GetOrderRequest{OrderId: "order-2"})
	require.NoError(t, err)
	assert.Equal(t, ordersv1.OrderPriority_ORDER_PRIORITY_STANDARD, standard.GetOrder().GetPriority())
	assert.Nil(t, standard.GetOrder().GetPremiumFee())
}

// FR-650: an unknown enum value is invalid input, not a standard Order.
func TestPlaceOrderRefusesUnknownPriority(t *testing.T) {
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
	client := newClient(t, subjects)
	request := placeRequest()
	request.Priority = ordersv1.OrderPriority(7)

	_, err := client.PlaceOrder(context.Background(), request)

	requireReason(t, err, codes.InvalidArgument, "INVALID_ARGUMENT")
}

var customer = authz.Subject{Kind: authz.SubjectUser, ID: "customer-1"}

func requireReason(t *testing.T, err error, code codes.Code, reason string) {
	t.Helper()
	require.Error(t, err)
	st := status.Convert(err)
	assert.Equal(t, code, st.Code())
	require.Len(t, st.Details(), 1)
	info, ok := st.Details()[0].(*errdetails.ErrorInfo)
	require.True(t, ok)
	assert.Equal(t, reason, info.GetReason())
}

func placeRequest() *ordersv1.PlaceOrderRequest {
	return &ordersv1.PlaceOrderRequest{
		IdempotencyKey: "key-1",
		RestaurantId:   "restaurant-1",
		Items:          []*ordersv1.PlaceOrderItem{{MenuItemId: "pizza", Quantity: 2}},
	}
}

// AC-508: PlaceOrder places the Order for the verified Customer and starts fulfillment.
func TestPlaceOrderCallsApplicationHandler(t *testing.T) {
	store, catalog := repositorytest.Open(t), NewMockCatalog(t)
	catalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza"}).Return(placeorder.CatalogSnapshot{
		MarketID:     ordertest.MarketID(t),
		Currency:     "EUR",
		Items:        map[order.MenuItemID]placeorder.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}},
		MinimumTotal: ordertest.Money(t, "0"),
		MaximumTotal: ordertest.Money(t, "100"),
	}, nil).Once()
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Times(2)
	client := newClientWithPorts(t, subjects, store, NewMockAuthorizer(t), commandPorts{
		catalog:  catalog,
		workflow: NewMockCancellationRequester(t),
		clock:    fake.NewClock(ordertest.PlacedAt),
	})

	response, err := client.PlaceOrder(context.Background(), placeRequest())
	require.NoError(t, err)
	assert.Equal(t, "order-1", response.GetOrderId())
	assert.False(t, response.GetReplayed())
	snapshot, ok := store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, order.CustomerID("customer-1"), snapshot.CustomerID)
	assert.Equal(t, []order.ID{"order-1"}, store.RelationshipChanges(), "orders-consumer syncs the relationships of the new Order")

	replayed, err := client.PlaceOrder(context.Background(), placeRequest())
	require.NoError(t, err)
	assert.Equal(t, "order-1", replayed.GetOrderId())
	assert.True(t, replayed.GetReplayed())
}

// AC-509: an unavailable catalog is UNAVAILABLE.
func TestPlaceOrderUnavailableCatalogIsUnavailable(t *testing.T) {
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza"}).
		Return(placeorder.CatalogSnapshot{}, fmt.Errorf("restaurants menu: %w", usecase.ErrDependencyUnavailable)).Once()
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
	client := newClientWithPorts(t, subjects, repositorytest.Open(t), NewMockAuthorizer(t), commandPorts{
		catalog:  catalog,
		workflow: NewMockCancellationRequester(t),
		clock:    fake.NewClock(ordertest.PlacedAt),
	})

	_, err := client.PlaceOrder(context.Background(), placeRequest())
	requireReason(t, err, codes.Unavailable, "DEPENDENCY_UNAVAILABLE")
}

// AC-566: refusals of the catalog name the restaurant rule; no Order is placed.
func TestPlaceOrderMapsRestaurantRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		code   codes.Code
		reason string
	}{
		"unknown restaurant": {placeorder.ErrUnknownRestaurant, codes.NotFound, "RESTAURANT_NOT_FOUND"},
		"closed restaurant":  {placeorder.ErrRestaurantNotAcceptingOrders, codes.FailedPrecondition, "RESTAURANT_NOT_ACCEPTING_ORDERS"},
	} {
		t.Run(name, func(t *testing.T) {
			store, catalog := repositorytest.Open(t), NewMockCatalog(t)
			catalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza"}).
				Return(placeorder.CatalogSnapshot{}, tc.err).Once()
			subjects := NewMockSubjectResolver(t)
			subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
			client := newClientWithPorts(t, subjects, store, NewMockAuthorizer(t), commandPorts{
				catalog:  catalog,
				workflow: NewMockCancellationRequester(t),
				clock:    fake.NewClock(ordertest.PlacedAt),
			})

			_, err := client.PlaceOrder(context.Background(), placeRequest())
			requireReason(t, err, tc.code, tc.reason)
			_, placed := store.Snapshot("order-1")
			assert.False(t, placed)
		})
	}
}

// AC-567: an unavailable Menu Item is ORDER_NOT_ORDERABLE with its code and id.
func TestPlaceOrderNamesUnorderableItems(t *testing.T) {
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "menu-gyoza"}).Return(placeorder.CatalogSnapshot{
		MarketID: ordertest.MarketID(t),
		Currency: "EUR",
		Items: map[order.MenuItemID]placeorder.CatalogItem{
			"pizza":      {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
			"menu-gyoza": {UnitPrice: ordertest.Money(t, "4.00"), Available: false},
		},
		MinimumTotal: ordertest.Money(t, "0"),
		MaximumTotal: ordertest.Money(t, "100"),
	}, nil).Once()
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
	client := newClientWithPorts(t, subjects, repositorytest.Open(t), NewMockAuthorizer(t), commandPorts{
		catalog:  catalog,
		workflow: NewMockCancellationRequester(t),
		clock:    fake.NewClock(ordertest.PlacedAt),
	})
	request := placeRequest()
	request.Items = append(request.Items, &ordersv1.PlaceOrderItem{MenuItemId: "menu-gyoza", Quantity: 1})

	_, err := client.PlaceOrder(context.Background(), request)
	requireReason(t, err, codes.FailedPrecondition, "ORDER_NOT_ORDERABLE")
	assert.Equal(t, map[string]string{"codes": "ORDER_ITEM_UNAVAILABLE", "menu_item_ids": "menu-gyoza"}, errorInfo(t, err).GetMetadata())
}

func TestPlaceOrderNamesEveryReasonCodeOnce(t *testing.T) {
	// The catalog knows only pizza of the requested items.
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "menu-gyoza", "menu-tea"}).Return(placeorder.CatalogSnapshot{
		MarketID:     ordertest.MarketID(t),
		Currency:     "EUR",
		Items:        map[order.MenuItemID]placeorder.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}},
		MinimumTotal: ordertest.Money(t, "0"),
		MaximumTotal: ordertest.Money(t, "10"),
	}, nil).Once()
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
	client := newClientWithPorts(t, subjects, repositorytest.Open(t), NewMockAuthorizer(t), commandPorts{
		catalog:  catalog,
		workflow: NewMockCancellationRequester(t),
		clock:    fake.NewClock(ordertest.PlacedAt),
	})
	request := placeRequest()
	request.Items = append(request.Items,
		&ordersv1.PlaceOrderItem{MenuItemId: "menu-gyoza", Quantity: 1},
		&ordersv1.PlaceOrderItem{MenuItemId: "menu-tea", Quantity: 1})

	_, err := client.PlaceOrder(context.Background(), request)
	requireReason(t, err, codes.FailedPrecondition, "ORDER_NOT_ORDERABLE")
	metadata := errorInfo(t, err).GetMetadata()
	assert.ElementsMatch(t, []string{"ORDER_ITEM_UNAVAILABLE", "ORDER_TOTAL_ABOVE_MAXIMUM"}, strings.Split(metadata["codes"], ","))
	assert.ElementsMatch(t, []string{"menu-gyoza", "menu-tea"}, strings.Split(metadata["menu_item_ids"], ","))
}

func errorInfo(t *testing.T, err error) *errdetails.ErrorInfo {
	t.Helper()
	details := status.Convert(err).Details()
	require.Len(t, details, 1)
	info, ok := details[0].(*errdetails.ErrorInfo)
	require.True(t, ok)
	return info
}

// AC-510: a service subject is not a Customer.
func TestPlaceOrderDeniesServiceSubject(t *testing.T) {
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(authz.Subject{Kind: authz.SubjectService, ID: "bff"}, nil).Once()
	client := newClient(t, subjects)

	_, err := client.PlaceOrder(context.Background(), placeRequest())
	requireReason(t, err, codes.PermissionDenied, "OPERATION_NOT_ALLOWED")
}

// AC-511: malformed composition is INVALID_ORDER.
func TestPlaceOrderMapsInvalidOrder(t *testing.T) {
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Times(3)
	client := newClient(t, subjects)
	for name, mutate := range map[string]func(*ordersv1.PlaceOrderRequest){
		"no items":  func(r *ordersv1.PlaceOrderRequest) { r.Items = nil },
		"duplicate": func(r *ordersv1.PlaceOrderRequest) { r.Items = append(r.Items, r.Items[0]) },
		"quantity":  func(r *ordersv1.PlaceOrderRequest) { r.Items[0].Quantity = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			request := placeRequest()
			mutate(request)
			_, err := client.PlaceOrder(context.Background(), request)
			requireReason(t, err, codes.InvalidArgument, "INVALID_ORDER")
		})
	}
}

func cancellationClient(t *testing.T, state order.State, subjects grpcadapter.SubjectResolver, ports commandPorts, authorizer *MockAuthorizer) ordersv1.OrdersServiceClient {
	t.Helper()
	store := repositorytest.Open(t)
	o := ordertest.New(t, "order-1", ordertest.WithState(state))
	require.NoError(t, store.Add(context.Background(), o))
	return newClientWithPorts(t, subjects, store, authorizer, ports)
}

func cancellationRequest() *ordersv1.RequestOrderCancellationRequest {
	return &ordersv1.RequestOrderCancellationRequest{OrderId: "order-1", RequestId: "request-1", Reason: "changed mind"}
}

// AC-512: RequestOrderCancellation hands the request to the workflow.
func TestRequestOrderCancellationCallsApplicationHandler(t *testing.T) {
	workflow := NewMockCancellationRequester(t)
	workflow.EXPECT().RequestCancellation(mock.Anything, fulfillment.CancellationRequest{
		OrderID:     "order-1",
		RequestID:   "request-1",
		RequestedBy: customer,
		RequestedAt: ordertest.PlacedAt,
		Reason:      "changed mind",
	}).Return(nil).Once()
	subjects := NewMockSubjectResolver(t)
	subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
	client := cancellationClient(t, order.StatePlaced, subjects, commandPorts{
		catalog:  NewMockCatalog(t),
		workflow: workflow,
		clock:    fake.NewClock(ordertest.PlacedAt),
	}, NewMockAuthorizer(t))

	response, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
	require.NoError(t, err)
	assert.Equal(t, ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_REQUESTED, response.GetOutcome())

	// The cancelled Order does not reach the workflow.
	cancelledSubjects := NewMockSubjectResolver(t)
	cancelledSubjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
	cancelled := cancellationClient(t, order.StateCancelled, cancelledSubjects, commandPorts{
		catalog:  NewMockCatalog(t),
		workflow: NewMockCancellationRequester(t),
		clock:    fake.NewClock(ordertest.PlacedAt),
	}, NewMockAuthorizer(t))
	response, err = cancelled.RequestOrderCancellation(context.Background(), cancellationRequest())
	require.NoError(t, err)
	assert.Equal(t, ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_ALREADY_CANCELLED, response.GetOutcome())
}

// AC-513: state conflicts, closed windows and refusals are FAILED_PRECONDITION;
// an absent workflow or SpiceDB outage is UNAVAILABLE.
func TestRequestOrderCancellationMapsErrors(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		subjects := NewMockSubjectResolver(t)
		subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
		client := cancellationClient(t, order.StateCompleted, subjects, commandPorts{
			catalog:  NewMockCatalog(t),
			workflow: NewMockCancellationRequester(t),
			clock:    fake.NewClock(ordertest.PlacedAt),
		}, NewMockAuthorizer(t))
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.FailedPrecondition, "ORDER_STATE_CONFLICT")
	})
	t.Run("window expired", func(t *testing.T) {
		clock := fake.NewClock(ordertest.PlacedAt)
		clock.Advance(cancellationWindow + time.Second)
		subjects := NewMockSubjectResolver(t)
		subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
		client := cancellationClient(t, order.StatePlaced, subjects, commandPorts{
			catalog:  NewMockCatalog(t),
			workflow: NewMockCancellationRequester(t),
			clock:    clock,
		}, NewMockAuthorizer(t))
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		// AC-562: the closed window is told apart from a finished Order.
		requireReason(t, err, codes.FailedPrecondition, "CANCELLATION_WINDOW_EXPIRED")
	})
	t.Run("refused", func(t *testing.T) {
		workflow := NewMockCancellationRequester(t)
		workflow.EXPECT().RequestCancellation(mock.Anything, fulfillment.CancellationRequest{
			OrderID:     "order-1",
			RequestID:   "request-1",
			RequestedBy: customer,
			RequestedAt: ordertest.PlacedAt,
			Reason:      "changed mind",
		}).Return(fulfillment.ErrCancellationRefused).Once()
		subjects := NewMockSubjectResolver(t)
		subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
		client := cancellationClient(t, order.StatePlaced, subjects, commandPorts{
			catalog:  NewMockCatalog(t),
			workflow: workflow,
			clock:    fake.NewClock(ordertest.PlacedAt),
		}, NewMockAuthorizer(t))
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.FailedPrecondition, "ORDER_STATE_CONFLICT")
	})
	t.Run("workflow not found", func(t *testing.T) {
		workflow := NewMockCancellationRequester(t)
		workflow.EXPECT().RequestCancellation(mock.Anything, fulfillment.CancellationRequest{
			OrderID:     "order-1",
			RequestID:   "request-1",
			RequestedBy: customer,
			RequestedAt: ordertest.PlacedAt,
			Reason:      "changed mind",
		}).Return(fulfillment.ErrWorkflowNotFound).Once()
		subjects := NewMockSubjectResolver(t)
		subjects.EXPECT().Resolve(mock.Anything).Return(customer, nil).Once()
		client := cancellationClient(t, order.StatePlaced, subjects, commandPorts{
			catalog:  NewMockCatalog(t),
			workflow: workflow,
			clock:    fake.NewClock(ordertest.PlacedAt),
		}, NewMockAuthorizer(t))
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.Unavailable, "DEPENDENCY_UNAVAILABLE")
	})
	t.Run("spicedb unavailable", func(t *testing.T) {
		// Only a subject other than the Customer is checked in SpiceDB.
		supportConsole := authz.Subject{Kind: authz.SubjectService, ID: "support-console"}
		authorizer := NewMockAuthorizer(t)
		authorizer.EXPECT().Check(mock.Anything, authz.AccessCheck{
			Subject:    supportConsole,
			Permission: requestordercancellation.PermissionCancel,
			Resource:   authz.Resource{Type: requestordercancellation.ResourceOrder, ID: "order-1"},
		}).Return(spicedb.ErrUnavailable).Once()
		store := repositorytest.Open(t)
		require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
		subjects := NewMockSubjectResolver(t)
		subjects.EXPECT().Resolve(mock.Anything).Return(supportConsole, nil).Once()
		client := newClientWithPorts(t, subjects, store, authorizer, commandPorts{
			catalog:  NewMockCatalog(t),
			workflow: NewMockCancellationRequester(t),
			clock:    fake.NewClock(ordertest.PlacedAt),
		})
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.Unavailable, "DEPENDENCY_UNAVAILABLE")
	})
}

// fixedWindow is the production cancellation policy with the given window.
func fixedWindow(t *testing.T, window time.Duration) cancellationpolicy.Fixed {
	t.Helper()
	policy, err := cancellationpolicy.NewFixed(window, time.Hour)
	require.NoError(t, err)
	return policy
}
