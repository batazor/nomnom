package market

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"buf.build/gen/go/nomnom/restaurants/grpc/go/nomnom/restaurants/v1/restaurantsv1grpc"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
)

// restaurants is the context of the Cell: it counts calls and records the
// metadata it received.
type restaurants struct {
	restaurantsv1grpc.UnimplementedRestaurantsServiceServer
	mu     sync.Mutex
	calls  int
	seenMD metadata.MD
}

func (r *restaurants) GetRestaurant(ctx context.Context, _ *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	r.mu.Lock()
	r.calls++
	r.seenMD = md
	r.mu.Unlock()
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{Name: "Pizza"}}, nil
}

func client(t *testing.T) (restaurantsv1grpc.RestaurantsServiceClient, *restaurants) {
	t.Helper()
	server := &restaurants{}
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	restaurantsv1grpc.RegisterRestaurantsServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return restaurantsv1grpc.NewRestaurantsServiceClient(Require(conn)), server
}

func inMarket(values ...string) context.Context {
	md := metadata.MD{}
	for _, value := range values {
		md.Append(Metadata, value)
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

func refusalCode(t *testing.T, err error) codes.Code {
	t.Helper()
	refusal, ok := errors.AsType[*Refusal](err)
	require.True(t, ok, "want a Refusal, got %v", err)
	// Not a gRPC status: adapters must not read it as the owner's answer.
	assert.Equal(t, codes.Unknown, status.Code(err))
	assert.Equal(t, refusal.Code, status.Code(refusal.Err()))
	return refusal.Code
}

func TestACallWithAMarketReachesTheContext(t *testing.T) {
	restaurantsClient, server := client(t)

	response, err := restaurantsClient.GetRestaurant(inMarket("berlin"), &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	require.NoError(t, err)
	assert.Equal(t, "Pizza", response.GetRestaurant().GetName())
	assert.Empty(t, server.seenMD.Get(Metadata), "the Market is not forwarded to the context as metadata")
}

func TestMarketHeaderMustBeOneValidSlug(t *testing.T) {
	restaurantsClient, server := client(t)

	for name, ctx := range map[string]context.Context{
		"missing":        context.Background(),
		"twice":          inMarket("berlin", "warsaw"),
		"empty":          inMarket(""),
		"upper case":     inMarket("Berlin"),
		"leading digit":  inMarket("1berlin"),
		"one character":  inMarket("b"),
		"too long":       inMarket("a23456789012345678901234567890123"),
		"comma-joined":   inMarket("berlin,warsaw"),
		"with separator": inMarket("berlin_1"),
	} {
		_, err := restaurantsClient.GetRestaurant(ctx, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
		assert.Equal(t, codes.InvalidArgument, refusalCode(t, err), name)
	}
	assert.Zero(t, server.calls, "a refused request reaches no context")
}

func TestFromAcceptsTheSlug(t *testing.T) {
	got, err := From(inMarket("frankfurt-am-main"))
	require.NoError(t, err)
	assert.Equal(t, "frankfurt-am-main", got)
}
