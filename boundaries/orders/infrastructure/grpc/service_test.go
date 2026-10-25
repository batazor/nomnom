package grpcadapter_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	grpcadapter "nomnom/orders/infrastructure/grpc"
	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/placeorder"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
	"nomnom/orders/slices/order/infrastructure/neighbours"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

type fixedSubject struct {
	subject application.Subject
	err     error
}

func (r fixedSubject) Resolve(context.Context) (application.Subject, error) {
	return r.subject, r.err
}

const cancellationWindow = 5 * time.Minute

func newClient(t *testing.T, subjects grpcadapter.SubjectResolver) ordersv1.OrdersServiceClient {
	t.Helper()
	return newClientWith(t, subjects, memory.NewStore(), memory.NewAuthorizer())
}

func newClientWith(t *testing.T, subjects grpcadapter.SubjectResolver, store *memory.Store, authorizer *memory.Authorizer) ordersv1.OrdersServiceClient {
	t.Helper()
	return newClientWithPorts(t, subjects, store, authorizer, newCommandPorts())
}

// commandPorts are the ports of the command use cases besides store and authorizer.
type commandPorts struct {
	catalog  placeorder.Catalog
	workflow *memory.Workflow
	clock    *memory.Clock
}

func newCommandPorts() commandPorts {
	return commandPorts{catalog: memory.NewCatalog(), workflow: memory.NewWorkflow(), clock: memory.NewClock(ordertest.PlacedAt)}
}

func newClientWithPorts(t *testing.T, subjects grpcadapter.SubjectResolver, store *memory.Store, authorizer *memory.Authorizer, ports commandPorts) ordersv1.OrdersServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	module := orderdi.NewModule(store, authorizer, memory.FixedWindow{Duration: cancellationWindow},
		ports.catalog, ports.workflow, ports.clock, &memory.IDs{})
	ordersv1.RegisterOrdersServiceServer(server, grpcadapter.NewService(module, subjects))
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
	client := newClient(t, fixedSubject{subject: application.Subject{
		Kind: application.SubjectUser,
		ID:   "customer-1",
	}})

	response, err := client.ListCustomerOrders(context.Background(), &ordersv1.ListCustomerOrdersRequest{
		CustomerId: "customer-1",
	})

	require.NoError(t, err)
	assert.Empty(t, response.GetOrders())
	assert.Empty(t, response.GetNextPageToken())
}

func TestServiceReturnsStructuredInvalidArgument(t *testing.T) {
	client := newClient(t, fixedSubject{subject: application.Subject{
		Kind: application.SubjectUser,
		ID:   "customer-1",
	}})

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

func TestServiceRejectsUnverifiedCallerByDefault(t *testing.T) {
	client := newClient(t, grpcadapter.NewRejectingSubjectResolver())

	_, err := client.ListCustomerOrders(context.Background(), &ordersv1.ListCustomerOrdersRequest{
		CustomerId: "customer-1",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// AC-504, AC-505: GetOrder carries the window only while the Order may be cancelled.
func TestGetOrderMapsCancellationWindow(t *testing.T) {
	store := memory.NewStore()
	authorizer := memory.NewAuthorizer()
	for id, state := range map[order.ID]order.State{"order-placed": order.StatePlaced, "order-completed": order.StateCompleted} {
		o := ordertest.New(t, id, ordertest.WithState(state))
		require.NoError(t, store.Add(context.Background(), o))
		_, err := authorizer.GrantOrderRelationships(context.Background(), o.ID(), o.CustomerID(), o.RestaurantID())
		require.NoError(t, err)
	}
	client := newClientWith(t, fixedSubject{subject: application.Subject{
		Kind: application.SubjectUser,
		ID:   "customer-1",
	}}, store, authorizer)

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

var customer = application.Subject{Kind: application.SubjectUser, ID: "customer-1"}

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
		IdempotencyKey:  "key-1",
		RestaurantId:    "restaurant-1",
		Items:           []*ordersv1.PlaceOrderItem{{MenuItemId: "pizza", Quantity: 2}},
		DeliveryAddress: "Main street 1",
	}
}

// AC-508: PlaceOrder places the Order for the verified Customer and starts fulfillment.
func TestPlaceOrderCallsApplicationHandler(t *testing.T) {
	store, authorizer, ports := memory.NewStore(), memory.NewAuthorizer(), newCommandPorts()
	catalog := memory.NewCatalog()
	catalog.Put("restaurant-1", placeorder.CatalogSnapshot{
		Currency:     "EUR",
		Items:        map[order.MenuItemID]placeorder.CatalogItem{"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true}},
		MinimumTotal: ordertest.Money(t, "0"),
		MaximumTotal: ordertest.Money(t, "100"),
	})
	ports.catalog = catalog
	client := newClientWithPorts(t, fixedSubject{subject: customer}, store, authorizer, ports)

	response, err := client.PlaceOrder(context.Background(), placeRequest())
	require.NoError(t, err)
	assert.Equal(t, "order-1", response.GetOrderId())
	assert.False(t, response.GetReplayed())
	assert.True(t, response.GetFulfillmentStarted())
	snapshot, ok := store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, order.CustomerID("customer-1"), snapshot.CustomerID)
	assert.Equal(t, order.RestaurantID("restaurant-1"), authorizer.RestaurantOf("order-1"))

	replayed, err := client.PlaceOrder(context.Background(), placeRequest())
	require.NoError(t, err)
	assert.Equal(t, "order-1", replayed.GetOrderId())
	assert.True(t, replayed.GetReplayed())
}

// AC-509: the production catalog fails closed while restaurants has no menu API.
func TestPlaceOrderUnavailableCatalogIsUnavailable(t *testing.T) {
	ports := newCommandPorts()
	ports.catalog = neighbours.UnavailableCatalog{}
	client := newClientWithPorts(t, fixedSubject{subject: customer}, memory.NewStore(), memory.NewAuthorizer(), ports)

	_, err := client.PlaceOrder(context.Background(), placeRequest())
	requireReason(t, err, codes.Unavailable, "DEPENDENCY_UNAVAILABLE")
}

// AC-510: a service subject is not a Customer.
func TestPlaceOrderDeniesServiceSubject(t *testing.T) {
	client := newClient(t, fixedSubject{subject: application.Subject{Kind: application.SubjectService, ID: "bff"}})

	_, err := client.PlaceOrder(context.Background(), placeRequest())
	requireReason(t, err, codes.PermissionDenied, "OPERATION_NOT_ALLOWED")
}

// AC-511: malformed composition and Delivery Address are INVALID_ORDER.
func TestPlaceOrderMapsInvalidOrder(t *testing.T) {
	client := newClient(t, fixedSubject{subject: customer})
	for name, mutate := range map[string]func(*ordersv1.PlaceOrderRequest){
		"no items":  func(r *ordersv1.PlaceOrderRequest) { r.Items = nil },
		"duplicate": func(r *ordersv1.PlaceOrderRequest) { r.Items = append(r.Items, r.Items[0]) },
		"quantity":  func(r *ordersv1.PlaceOrderRequest) { r.Items[0].Quantity = 0 },
		"address":   func(r *ordersv1.PlaceOrderRequest) { r.DeliveryAddress = " " },
	} {
		t.Run(name, func(t *testing.T) {
			request := placeRequest()
			mutate(request)
			_, err := client.PlaceOrder(context.Background(), request)
			requireReason(t, err, codes.InvalidArgument, "INVALID_ORDER")
		})
	}
}

func cancellationClient(t *testing.T, state order.State, ports commandPorts, authorizer *memory.Authorizer) ordersv1.OrdersServiceClient {
	t.Helper()
	store := memory.NewStore()
	o := ordertest.New(t, "order-1", ordertest.WithState(state))
	require.NoError(t, store.Add(context.Background(), o))
	_, err := authorizer.GrantOrderRelationships(context.Background(), o.ID(), o.CustomerID(), o.RestaurantID())
	require.NoError(t, err)
	return newClientWithPorts(t, fixedSubject{subject: customer}, store, authorizer, ports)
}

func cancellationRequest() *ordersv1.RequestOrderCancellationRequest {
	return &ordersv1.RequestOrderCancellationRequest{OrderId: "order-1", RequestId: "request-1", Reason: "changed mind"}
}

// AC-512: RequestOrderCancellation hands the request to the workflow.
func TestRequestOrderCancellationCallsApplicationHandler(t *testing.T) {
	ports := newCommandPorts()
	client := cancellationClient(t, order.StatePlaced, ports, memory.NewAuthorizer())

	response, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
	require.NoError(t, err)
	assert.Equal(t, ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_REQUESTED, response.GetOutcome())
	require.Len(t, ports.workflow.Cancellations(), 1)
	assert.Equal(t, customer, ports.workflow.Cancellations()[0].RequestedBy)

	cancelled := cancellationClient(t, order.StateCancelled, newCommandPorts(), memory.NewAuthorizer())
	response, err = cancelled.RequestOrderCancellation(context.Background(), cancellationRequest())
	require.NoError(t, err)
	assert.Equal(t, ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_ALREADY_CANCELLED, response.GetOutcome())
}

// AC-513: state conflicts, closed windows and refusals are FAILED_PRECONDITION;
// an absent workflow or SpiceDB outage is UNAVAILABLE.
func TestRequestOrderCancellationMapsErrors(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		client := cancellationClient(t, order.StateCompleted, newCommandPorts(), memory.NewAuthorizer())
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.FailedPrecondition, "ORDER_STATE_CONFLICT")
	})
	t.Run("window expired", func(t *testing.T) {
		ports := newCommandPorts()
		ports.clock.Advance(cancellationWindow + time.Second)
		client := cancellationClient(t, order.StatePlaced, ports, memory.NewAuthorizer())
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.FailedPrecondition, "ORDER_STATE_CONFLICT")
	})
	t.Run("refused", func(t *testing.T) {
		ports := newCommandPorts()
		ports.workflow.FailCancellation(fulfillment.ErrCancellationRefused)
		client := cancellationClient(t, order.StatePlaced, ports, memory.NewAuthorizer())
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.FailedPrecondition, "ORDER_STATE_CONFLICT")
	})
	t.Run("workflow not found", func(t *testing.T) {
		ports := newCommandPorts()
		ports.workflow.FailCancellation(fulfillment.ErrWorkflowNotFound)
		client := cancellationClient(t, order.StatePlaced, ports, memory.NewAuthorizer())
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.Unavailable, "DEPENDENCY_UNAVAILABLE")
	})
	t.Run("spicedb unavailable", func(t *testing.T) {
		authorizer := memory.NewAuthorizer()
		client := cancellationClient(t, order.StatePlaced, newCommandPorts(), authorizer)
		authorizer.FailChecks(spicedb.ErrUnavailable)
		_, err := client.RequestOrderCancellation(context.Background(), cancellationRequest())
		requireReason(t, err, codes.Unavailable, "DEPENDENCY_UNAVAILABLE")
	})
}
