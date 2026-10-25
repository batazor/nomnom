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
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
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
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	module := orderdi.NewModule(store, authorizer, memory.FixedWindow{Duration: cancellationWindow})
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
		_, err := authorizer.GrantOrderCustomer(context.Background(), o.ID(), o.CustomerID())
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
