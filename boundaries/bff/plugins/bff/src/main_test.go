package main

import (
	"context"
	"net"
	"testing"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const bufferSize = 1024 * 1024

func TestQueryBffInfo(t *testing.T) {
	response, err := newClient(t).QueryBffInfo(
		context.Background(),
		&service.QueryBffInfoRequest{},
	)
	require.NoError(t, err)
	require.Equal(t, "nomnom-bff", response.BffInfo.Name)
	require.Equal(t, serviceVersion, response.BffInfo.Version)
}

// Customer operations are published in the schema ahead of their resolvers.
func TestCustomerOperationsAreNotImplementedYet(t *testing.T) {
	_, err := newClient(t).QueryOrder(
		context.Background(),
		&service.QueryOrderRequest{Id: "order-received"},
	)
	require.Equal(t, codes.Unimplemented, status.Code(err))
}

func newClient(t *testing.T) service.BffServiceClient {
	t.Helper()
	listener := bufconn.Listen(bufferSize)
	server := grpc.NewServer()
	service.RegisterBffServiceServer(server, &BffService{})

	serveError := make(chan error, 1)
	go func() {
		serveError <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, <-serveError)
	})

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	return service.NewBffServiceClient(connection)
}
