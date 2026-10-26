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

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courierpool/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/cellrouter"
	deliveryv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/delivery/v1"
)

const fixtures = "../../../../../../tests/identity/fixtures"

// deliveryServer answers the Courier Pool RPCs and records every call and
// whether it carried a user assertion.
type deliveryServer struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	err      error
	markets  []string
	calls    []string
	asserted []bool
}

func (s *deliveryServer) record(ctx context.Context, call string) error {
	md, _ := metadata.FromIncomingContext(ctx)
	s.calls = append(s.calls, call)
	s.asserted = append(s.asserted, len(md.Get(identity.AssertionMetadata)) == 1)
	return s.err
}

func (s *deliveryServer) GetCourierPool(ctx context.Context, request *deliveryv1.GetCourierPoolRequest) (*deliveryv1.GetCourierPoolResponse, error) {
	s.markets = append(s.markets, request.GetMarketId())
	if err := s.record(ctx, "pool"); err != nil {
		return nil, err
	}
	return &deliveryv1.GetCourierPoolResponse{Size: 2, OnLine: 2, Busy: 1, Waiting: 3, Couriers: []*deliveryv1.PoolCourier{
		{CourierId: "courier-1", Name: "Анна", Status: deliveryv1.CourierStatus_COURIER_STATUS_AVAILABLE, PendingOfferId: "offer-1", OfferedOrderId: "order-2"},
		{CourierId: "courier-2", Name: "Тимур", Status: deliveryv1.CourierStatus_COURIER_STATUS_BUSY, ActiveDeliveryId: "delivery-1", ActiveOrderId: "order-1"},
	}}, nil
}

func (s *deliveryServer) RegisterCourier(ctx context.Context, r *deliveryv1.RegisterCourierRequest) (*deliveryv1.RegisterCourierResponse, error) {
	s.markets = append(s.markets, r.GetMarketId())
	return &deliveryv1.RegisterCourierResponse{}, s.record(ctx, "register "+r.GetCourierId()+" "+r.GetName())
}

func (s *deliveryServer) UpdateCourierLocation(ctx context.Context, r *deliveryv1.UpdateCourierLocationRequest) (*deliveryv1.UpdateCourierLocationResponse, error) {
	return &deliveryv1.UpdateCourierLocationResponse{}, s.record(ctx, "place "+r.GetCourierId())
}

func (s *deliveryServer) SetCourierAvailability(ctx context.Context, r *deliveryv1.SetCourierAvailabilityRequest) (*deliveryv1.SetCourierAvailabilityResponse, error) {
	return &deliveryv1.SetCourierAvailabilityResponse{}, s.record(ctx, r.GetAvailability().String()+" "+r.GetCourierId())
}

func (s *deliveryServer) RetireCourier(ctx context.Context, r *deliveryv1.RetireCourierRequest) (*deliveryv1.RetireCourierResponse, error) {
	return &deliveryv1.RetireCourierResponse{}, s.record(ctx, "retire "+r.GetCourierId())
}

func connect(t *testing.T, server *deliveryServer) *Delivery {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	deliveryv1.RegisterDeliveryServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nomnom.delivery"), []byte("token-nomnom.delivery"), 0o600))
	signer, err := identity.NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "nomnom-bff-test-key")
	require.NoError(t, err)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(identity.UnaryClientInterceptor("nomnom.delivery", identity.ServiceTokens{Dir: dir}, signer)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewDelivery(conn)
}

// dispatcherContext is a request of the dispatcher in Market berlin.
func dispatcherContext() context.Context {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(cellrouter.MarketMetadata, "berlin"))
	return identity.WithUser(ctx, identity.User{ID: "dispatcher"})
}

func TestPoolIsReadOnBehalfOfTheDispatcher(t *testing.T) {
	server := &deliveryServer{}
	pool, err := connect(t, server).Pool(dispatcherContext())

	require.NoError(t, err)
	assert.Equal(t, applications.Pool{Market: "berlin", Size: 2, OnLine: 2, Busy: 1, Waiting: 3, Couriers: []applications.Courier{
		{ID: "courier-1", Name: "Анна", Availability: applications.AvailabilityAvailable, OfferedOrderID: "order-2"},
		{ID: "courier-2", Name: "Тимур", Availability: applications.AvailabilityBusy, CurrentOrderID: "order-1"},
	}}, pool)
	assert.Equal(t, []bool{true}, server.asserted)
	assert.Equal(t, []string{"berlin"}, server.markets, "the pool is of the request Market")

	server.err = status.Error(codes.PermissionDenied, "no")
	_, err = connect(t, server).Pool(dispatcherContext())
	assert.ErrorIs(t, err, applications.ErrNotPermitted)
}

func TestCommandsGoToTheirRPCAndNameRefusals(t *testing.T) {
	server := &deliveryServer{}
	delivery := connect(t, server)
	require.NoError(t, delivery.Register(dispatcherContext(), "courier-3", "Сергей"))
	require.NoError(t, delivery.Place(dispatcherContext(), "courier-3", applications.Location{Latitude: 52.5, Longitude: 13.4}))
	require.NoError(t, delivery.SetOnLine(dispatcherContext(), "courier-3", true))
	require.NoError(t, delivery.SetOnLine(dispatcherContext(), "courier-3", false))
	require.NoError(t, delivery.Retire(dispatcherContext(), "courier-3"))
	assert.Equal(t, []string{
		"register courier-3 Сергей", "place courier-3",
		"COURIER_AVAILABILITY_AVAILABLE courier-3", "COURIER_AVAILABILITY_UNAVAILABLE courier-3",
		"retire courier-3",
	}, server.calls)
	assert.Equal(t, []string{"berlin"}, server.markets, "a new courier joins the pool of the request Market")

	refused := func(code codes.Code, call func() error, want error) {
		server.err = status.Error(code, "refused")
		assert.ErrorIs(t, call(), want, code)
	}
	refused(codes.InvalidArgument, func() error { return delivery.Register(dispatcherContext(), "courier-4", " ") }, applications.ErrInvalidCourierName)
	refused(codes.FailedPrecondition, func() error { return delivery.SetOnLine(dispatcherContext(), "courier-1", false) }, applications.ErrStatusConflict)
	refused(codes.FailedPrecondition, func() error { return delivery.Retire(dispatcherContext(), "courier-1") }, applications.ErrCourierOnLine)
	refused(codes.NotFound, func() error { return delivery.Retire(dispatcherContext(), "courier-9") }, applications.ErrCourierNotFound)
	refused(codes.PermissionDenied, func() error { return delivery.Retire(dispatcherContext(), "courier-1") }, applications.ErrNotPermitted)
}
