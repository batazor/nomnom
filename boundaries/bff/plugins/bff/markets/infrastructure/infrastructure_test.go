package infrastructure

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	controlplanev1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/controlplane/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/markets/applications"
)

const fixtures = "../../../../../../tests/identity/fixtures"

type controlPlaneServer struct {
	controlplanev1.UnimplementedControlPlaneServiceServer
	err    error
	seenMD metadata.MD
}

func (s *controlPlaneServer) ListMarkets(ctx context.Context, _ *controlplanev1.ListMarketsRequest) (*controlplanev1.ListMarketsResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	if s.err != nil {
		return nil, s.err
	}
	return &controlplanev1.ListMarketsResponse{Markets: []*controlplanev1.Market{
		{MarketId: "berlin", Name: "Berlin", Currency: "EUR", CellId: "cell-1"},
		{MarketId: "warsaw", Name: "Warsaw", Currency: "PLN", CellId: "cell-2"},
	}}, nil
}

func connect(t *testing.T, server *controlPlaneServer) *ControlPlane {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	controlplanev1.RegisterControlPlaneServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nomnom.controlplane"), []byte("token-nomnom.controlplane"), 0o600))
	signer, err := identity.NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "nomnom-bff-test-key")
	require.NoError(t, err)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(identity.UnaryClientInterceptor("nomnom.controlplane", identity.ServiceTokens{Dir: dir}, signer)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewControlPlane(conn)
}

// The Market list is read as BFF itself, even inside a customer request, and
// keeps the order of the control plane.
func TestMarketsAreReadAsTheServiceWithoutAUser(t *testing.T) {
	server := &controlPlaneServer{}
	ctx := identity.WithUser(context.Background(), identity.User{ID: "customer-1"})

	markets, err := connect(t, server).Markets(ctx)

	require.NoError(t, err)
	assert.Equal(t, []applications.Market{
		{ID: "berlin", Name: "Berlin", Currency: "EUR"},
		{ID: "warsaw", Name: "Warsaw", Currency: "PLN"},
	}, markets)
	assert.Equal(t, []string{"Bearer token-nomnom.controlplane"}, server.seenMD.Get("authorization"))
	assert.Empty(t, server.seenMD.Get(identity.AssertionMetadata))
}

func TestMarketsFailureIsAnError(t *testing.T) {
	_, err := connect(t, &controlPlaneServer{err: status.Error(codes.Unavailable, "down")}).Markets(context.Background())
	assert.Equal(t, codes.Unavailable, status.Code(err))
}
