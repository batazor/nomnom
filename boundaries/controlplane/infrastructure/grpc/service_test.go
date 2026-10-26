package grpcadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/batazor/nomnom/boundaries/controlplane/di/schema"
	grpcadapter "github.com/batazor/nomnom/boundaries/controlplane/infrastructure/grpc"
	controlplanev1 "github.com/batazor/nomnom/boundaries/controlplane/infrastructure/grpc/gen/nomnom/controlplane/v1"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config/database"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/testfixture"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/createmarket"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/listmarkets"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/registercell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/repository"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/spicedb"
)

const identityFixtures = "../../../../tests/identity/fixtures"

var (
	pg     *testfixture.Postgres
	authzd *testfixture.SpiceDB
)

func TestMain(m *testing.M) {
	pg = testfixture.MustStartPostgres()
	defer pg.Close()
	authzd = testfixture.MustStartSpiceDB()
	defer authzd.Close()
	m.Run()
}

// userVectors are the shared ADR 32 vectors: a BFF service token (delegate), a
// non-delegate service token (orders-worker) and user assertions.
type userVectors struct {
	Issuer           string            `json:"issuer"`
	Audience         string            `json:"audience"`
	Now              int64             `json:"now"`
	Delegates        string            `json:"delegates"`
	ServiceIssuer    string            `json:"service_issuer"`
	ServiceAccounts  string            `json:"service_accounts"`
	DelegateToken    string            `json:"delegate_token"`
	NonDelegateToken string            `json:"non_delegate_token"`
	ExpectedSubject  string            `json:"expected_subject"`
	Tokens           map[string]string `json:"tokens"`
}

// caller is the API through bufconn: the real identity resolver, use cases,
// PostgreSQL and SpiceDB; the asserted user of the vectors is the operator.
// The gateway is a mock that records the applied routes.
type caller struct {
	client  controlplanev1.ControlPlaneServiceClient
	v       userVectors
	gateway *gateway
}

// gateway is a mock of the Kubernetes API of the public gateway.
type gateway struct {
	applied []publishroutes.MarketRoute
	down    bool
}

func (g *gateway) Apply(_ context.Context, routes []publishroutes.MarketRoute) error {
	if g.down {
		return errors.New("apiserver down")
	}
	g.applied = routes
	return nil
}

func newCaller(t *testing.T) caller {
	t.Helper()
	ctx := context.Background()
	data, err := os.ReadFile(filepath.Join(identityFixtures, "user-vectors.json"))
	require.NoError(t, err)
	var v userVectors
	require.NoError(t, json.Unmarshal(data, &v))

	resolver, err := grpcadapter.NewSubjectResolver(config.IdentityConfig{
		Issuer: v.ServiceIssuer, Audience: v.Audience, JWKSFile: filepath.Join(identityFixtures, "jwks.json"),
		ServiceAccounts: v.ServiceAccounts, UserIssuer: v.Issuer, UserJWKSFile: filepath.Join(identityFixtures, "bff-jwks.json"),
		UserDelegates: v.Delegates,
	})
	require.NoError(t, err)
	grpcadapter.PinClock(resolver, time.Unix(v.Now, 0))

	spice, err := spicedb.Dial(config.SpiceDBConfig{Endpoint: authzd.Endpoint, PresharedKey: testfixture.SpiceDBPresharedKey, Insecure: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = spice.Close() })
	_, err = spice.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: []*v1.RelationshipUpdate{{
		Operation: v1.RelationshipUpdate_OPERATION_TOUCH,
		Relationship: &v1.Relationship{
			Resource: &v1.ObjectReference{ObjectType: "platform", ObjectId: "nomnom"},
			Relation: "operator",
			Subject:  &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: v.ExpectedSubject}},
		},
	}}})
	require.NoError(t, err)
	authorizer := spicedb.NewAuthorizer(spice, 5*time.Second)

	cfg := database.Config{DSN: pg.DSN, MaxConns: 4}
	require.NoError(t, schema.Migrate(ctx, cfg))
	store, err := repository.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	public := &gateway{}
	routes := publishroutes.New(repository.NewRoutes(store), public)
	service := grpcadapter.NewService(grpcadapter.Handlers{
		RegisterCell: registercell.New(authorizer, repository.NewCells(store), routes),
		CreateMarket: createmarket.New(authorizer, repository.NewMarkets(store), routes),
		ListMarkets:  listmarkets.New(authorizer, repository.NewMarkets(store)),
	}, resolver)

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	controlplanev1.RegisterControlPlaneServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return caller{client: controlplanev1.NewControlPlaneServiceClient(conn), v: v, gateway: public}
}

// operator calls as the asserted user through BFF.
func (c caller) operator() context.Context {
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"authorization", "Bearer "+c.v.DelegateToken, grpcadapter.UserAssertionMetadata, c.v.Tokens["valid"]))
}

// service calls with a service token only.
func service(token string) context.Context {
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func requireStatus(t *testing.T, err error, code codes.Code, reason string) {
	t.Helper()
	st, ok := status.FromError(err)
	require.True(t, ok, "%v", err)
	require.Equal(t, code, st.Code(), st.Message())
	if reason == "" {
		return
	}
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			assert.Equal(t, reason, info.GetReason())
			return
		}
	}
	t.Fatalf("no ErrorInfo in %v", st)
}

func registerCell(id string, port int32) *controlplanev1.RegisterCellRequest {
	return &controlplanev1.RegisterCellRequest{CellId: id, Router: &controlplanev1.ServiceRef{Namespace: "nomnom-bff", Name: "bff", Port: port}}
}

// One test owns the shared database: the topology is global, so the steps run in order.
func TestTopologyThroughTheAPI(t *testing.T) {
	c := newCaller(t)

	_, err := c.client.CreateMarket(c.operator(), &controlplanev1.CreateMarketRequest{MarketId: "berlin", Name: "Berlin", Currency: "EUR", CellId: "cell-1"})
	requireStatus(t, err, codes.FailedPrecondition, "CELL_NOT_FOUND")

	registered, err := c.client.RegisterCell(c.operator(), registerCell("cell-1", 3010))
	require.NoError(t, err)
	assert.True(t, registered.GetCreated())
	repeated, err := c.client.RegisterCell(c.operator(), registerCell("cell-1", 3010))
	require.NoError(t, err)
	assert.False(t, repeated.GetCreated())
	assert.Equal(t, "bff", repeated.GetCell().GetRouter().GetName())
	assert.Equal(t, int32(3010), repeated.GetCell().GetRouter().GetPort())
	_, err = c.client.RegisterCell(c.operator(), registerCell("cell-1", 3011))
	requireStatus(t, err, codes.AlreadyExists, "CELL_ALREADY_REGISTERED")
	_, err = c.client.RegisterCell(c.operator(), registerCell("Cell 1", 3010))
	requireStatus(t, err, codes.InvalidArgument, "INVALID_ARGUMENT")
	_, err = c.client.RegisterCell(c.operator(), &controlplanev1.RegisterCellRequest{CellId: "cell-2", Router: &controlplanev1.ServiceRef{Namespace: "nomnom-bff", Name: "Bff_1", Port: 3010}})
	requireStatus(t, err, codes.InvalidArgument, "INVALID_ARGUMENT")

	// The routes are applied after the write; a failed apply keeps the Market
	// and a repeat applies them.
	c.gateway.down = true
	_, err = c.client.CreateMarket(c.operator(), &controlplanev1.CreateMarketRequest{MarketId: "berlin", Name: " Berlin ", Currency: "EUR", CellId: "cell-1"})
	requireStatus(t, err, codes.Unavailable, "ROUTES_UNAVAILABLE")
	assert.Empty(t, c.gateway.applied)
	c.gateway.down = false
	again, err := c.client.CreateMarket(c.operator(), &controlplanev1.CreateMarketRequest{MarketId: "berlin", Name: "Berlin", Currency: "EUR", CellId: "cell-1"})
	require.NoError(t, err)
	assert.False(t, again.GetCreated(), "the Market stayed in the registry")
	assert.Equal(t, "Berlin", again.GetMarket().GetName())
	require.Len(t, c.gateway.applied, 1)
	assert.Equal(t, "berlin", string(c.gateway.applied[0].Market.ID()))
	assert.Equal(t, "nomnom-bff/bff:3010", c.gateway.applied[0].Router.String())
	_, err = c.client.CreateMarket(c.operator(), &controlplanev1.CreateMarketRequest{MarketId: "berlin", Name: "Berlin", Currency: "USD", CellId: "cell-1"})
	requireStatus(t, err, codes.AlreadyExists, "MARKET_ALREADY_EXISTS")

	markets, err := c.client.ListMarkets(c.operator(), &controlplanev1.ListMarketsRequest{})
	require.NoError(t, err)
	require.Len(t, markets.GetMarkets(), 1)
	assert.Equal(t, "EUR", markets.GetMarkets()[0].GetCurrency())

	// BFF without an operator may neither read nor write; an unauthenticated
	// caller gets nothing.
	_, err = c.client.ListMarkets(service(c.v.DelegateToken), &controlplanev1.ListMarketsRequest{})
	requireStatus(t, err, codes.PermissionDenied, "OPERATION_NOT_ALLOWED")
	_, err = c.client.RegisterCell(service(c.v.DelegateToken), registerCell("cell-2", 3010))
	requireStatus(t, err, codes.PermissionDenied, "OPERATION_NOT_ALLOWED")
	_, err = c.client.ListMarkets(context.Background(), &controlplanev1.ListMarketsRequest{})
	requireStatus(t, err, codes.Unauthenticated, "AUTHENTICATION_REQUIRED")
}
