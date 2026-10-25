package infrastructure

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/courier/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	deliveryv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/delivery/v1"
)

const fixtures = "../../../../../../tests/identity/fixtures"

// deliveryServer answers the courier RPCs and records the courier of every
// call and whether it carried a user assertion.
type deliveryServer struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	err       error
	status    deliveryv1.CourierStatus
	delivery  *deliveryv1.Delivery
	calls     []string
	asserted  []bool
	requested deliveryv1.CourierAvailability
}

func (s *deliveryServer) record(ctx context.Context, call string) error {
	md, _ := metadata.FromIncomingContext(ctx)
	s.calls = append(s.calls, call)
	s.asserted = append(s.asserted, len(md.Get(identity.AssertionMetadata)) == 1)
	return s.err
}

func (s *deliveryServer) GetCourierCurrentDelivery(ctx context.Context, r *deliveryv1.GetCourierCurrentDeliveryRequest) (*deliveryv1.GetCourierCurrentDeliveryResponse, error) {
	if err := s.record(ctx, "current "+r.GetCourierId()); err != nil {
		return nil, err
	}
	return &deliveryv1.GetCourierCurrentDeliveryResponse{Delivery: s.delivery, CourierStatus: s.status}, nil
}

func (s *deliveryServer) ListAvailableDeliveryOffers(ctx context.Context, r *deliveryv1.ListAvailableDeliveryOffersRequest) (*deliveryv1.ListAvailableDeliveryOffersResponse, error) {
	if err := s.record(ctx, "offers "+r.GetCourierId()); err != nil {
		return nil, err
	}
	return &deliveryv1.ListAvailableDeliveryOffersResponse{CourierId: r.GetCourierId(), Offers: []*deliveryv1.DeliveryOffer{{
		OfferId: "offer-1", DeliveryId: "delivery-1", OrderId: "order-1", PickupAddress: "ул. Садовая, 8", DropoffAddress: "ул. Лесная, 12",
		ExpiresAtEpochSeconds: time.Date(2026, 10, 25, 12, 5, 0, 0, time.UTC).Unix(),
	}}}, nil
}

func (s *deliveryServer) SetCourierAvailability(ctx context.Context, r *deliveryv1.SetCourierAvailabilityRequest) (*deliveryv1.SetCourierAvailabilityResponse, error) {
	s.requested = r.GetAvailability()
	return &deliveryv1.SetCourierAvailabilityResponse{}, s.record(ctx, "availability "+r.GetCourierId())
}

func (s *deliveryServer) AcceptDeliveryOffer(ctx context.Context, r *deliveryv1.AcceptDeliveryOfferRequest) (*deliveryv1.AcceptDeliveryOfferResponse, error) {
	return &deliveryv1.AcceptDeliveryOfferResponse{}, s.record(ctx, "accept "+r.GetCourierId()+" "+r.GetOfferId())
}

func (s *deliveryServer) ConfirmPickup(ctx context.Context, r *deliveryv1.ConfirmPickupRequest) (*deliveryv1.ConfirmPickupResponse, error) {
	return &deliveryv1.ConfirmPickupResponse{}, s.record(ctx, "pickup "+r.GetCourierId()+" "+r.GetDeliveryId())
}

func (s *deliveryServer) ConfirmDelivery(ctx context.Context, r *deliveryv1.ConfirmDeliveryRequest) (*deliveryv1.ConfirmDeliveryResponse, error) {
	return &deliveryv1.ConfirmDeliveryResponse{}, s.record(ctx, "delivery "+r.GetCourierId()+" "+r.GetDeliveryId())
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

func courierContext() context.Context {
	return identity.WithUser(context.Background(), identity.User{ID: "courier-1"})
}

func TestStateReadsStatusAndDeliveryOnBehalfOfTheCourier(t *testing.T) {
	server := &deliveryServer{
		status: deliveryv1.CourierStatus_COURIER_STATUS_BUSY,
		delivery: &deliveryv1.Delivery{
			DeliveryId: "delivery-1", OrderId: "order-1", CourierId: "courier-1",
			State: deliveryv1.DeliveryState_DELIVERY_STATE_PICKED_UP, PickupAddress: "ул. Садовая, 8", DropoffAddress: "ул. Лесная, 12",
		},
	}
	delivery := connect(t, server)

	state, err := delivery.State(courierContext(), "courier-1")

	require.NoError(t, err)
	assert.Equal(t, applications.CourierState{
		Availability: applications.AvailabilityBusy,
		CurrentDelivery: &applications.Delivery{
			ID: "delivery-1", OrderID: "order-1", Stage: applications.StagePickedUp,
			PickupAddress: "ул. Садовая, 8", DropoffAddress: "ул. Лесная, 12",
		},
	}, state)
	assert.Equal(t, []string{"current courier-1"}, server.calls)
	assert.Equal(t, []bool{true}, server.asserted)

	server.delivery, server.status = nil, deliveryv1.CourierStatus_COURIER_STATUS_UNAVAILABLE
	state, err = delivery.State(courierContext(), "courier-1")
	require.NoError(t, err)
	assert.Equal(t, applications.CourierState{Availability: applications.AvailabilityUnavailable}, state)

	server.status = deliveryv1.CourierStatus_COURIER_STATUS_UNSPECIFIED
	_, err = delivery.State(courierContext(), "courier-1")
	assert.Error(t, err)
}

func TestOffersMapExpiry(t *testing.T) {
	offers, err := connect(t, &deliveryServer{}).Offers(courierContext(), "courier-1")
	require.NoError(t, err)
	assert.Equal(t, []applications.Offer{{
		ID: "offer-1", DeliveryID: "delivery-1", OrderID: "order-1", PickupAddress: "ул. Садовая, 8", DropoffAddress: "ул. Лесная, 12",
		ExpiresAt: time.Date(2026, 10, 25, 12, 5, 0, 0, time.UTC),
	}}, offers)
}

func TestCommandsGoOnBehalfOfTheCourier(t *testing.T) {
	server := &deliveryServer{}
	delivery := connect(t, server)

	require.NoError(t, delivery.SetAvailability(courierContext(), "courier-1", applications.AvailabilityAvailable))
	assert.Equal(t, deliveryv1.CourierAvailability_COURIER_AVAILABILITY_AVAILABLE, server.requested)
	require.NoError(t, delivery.SetAvailability(courierContext(), "courier-1", applications.AvailabilityUnavailable))
	assert.Equal(t, deliveryv1.CourierAvailability_COURIER_AVAILABILITY_UNAVAILABLE, server.requested)
	require.NoError(t, delivery.AcceptOffer(courierContext(), "courier-1", "offer-1"))
	require.NoError(t, delivery.ConfirmPickup(courierContext(), "courier-1", "delivery-1"))
	require.NoError(t, delivery.ConfirmDelivery(courierContext(), "courier-1", "delivery-1"))

	assert.Equal(t, []string{"availability courier-1", "availability courier-1", "accept courier-1 offer-1", "pickup courier-1 delivery-1", "delivery courier-1 delivery-1"}, server.calls)
	assert.Equal(t, []bool{true, true, true, true, true}, server.asserted)
	assert.ErrorIs(t, delivery.SetAvailability(courierContext(), "courier-1", applications.AvailabilityBusy), applications.ErrUnknownCommand)
}

func TestCourierRefusals(t *testing.T) {
	server := &deliveryServer{}
	delivery := connect(t, server)
	refuse := func(code codes.Code) { server.err = status.Error(code, "refused") }

	for _, code := range []codes.Code{codes.NotFound, codes.PermissionDenied} {
		refuse(code)
		_, err := delivery.State(courierContext(), "courier-1")
		assert.ErrorIs(t, err, applications.ErrNotCourier, code)
	}
	refuse(codes.PermissionDenied)
	assert.ErrorIs(t, delivery.SetAvailability(courierContext(), "courier-1", applications.AvailabilityAvailable), applications.ErrNotCourier)
	assert.ErrorIs(t, delivery.AcceptOffer(courierContext(), "courier-1", "offer-1"), applications.ErrNotCourier)
	assert.ErrorIs(t, delivery.ConfirmPickup(courierContext(), "courier-1", "delivery-1"), applications.ErrNotCourier)

	refuse(codes.FailedPrecondition)
	assert.ErrorIs(t, delivery.SetAvailability(courierContext(), "courier-1", applications.AvailabilityUnavailable), applications.ErrAvailabilityConflict)
	assert.ErrorIs(t, delivery.AcceptOffer(courierContext(), "courier-1", "offer-1"), applications.ErrOfferNotAvailable)
	assert.ErrorIs(t, delivery.ConfirmPickup(courierContext(), "courier-1", "delivery-1"), applications.ErrStageConflict)
	assert.ErrorIs(t, delivery.ConfirmDelivery(courierContext(), "courier-1", "delivery-1"), applications.ErrStageConflict)

	refuse(codes.NotFound)
	assert.ErrorIs(t, delivery.AcceptOffer(courierContext(), "courier-1", "offer-1"), applications.ErrOfferNotAvailable)
	assert.ErrorIs(t, delivery.ConfirmDelivery(courierContext(), "courier-1", "delivery-1"), applications.ErrDeliveryNotFound)

	refuse(codes.InvalidArgument)
	assert.ErrorIs(t, delivery.ConfirmPickup(courierContext(), "courier-1", ""), applications.ErrInvalidCommand)

	refuse(codes.Unavailable)
	err := delivery.AcceptOffer(courierContext(), "courier-1", "offer-1")
	assert.Equal(t, codes.Unavailable, status.Code(errors.Unwrap(err)))
}
